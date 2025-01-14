// Copyright 2012, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package wrangler

import (
	"fmt"
	"sync"
	"time"

	"github.com/youtube/vitess/go/vt/concurrency"
	"github.com/youtube/vitess/go/vt/topo"
	"github.com/youtube/vitess/go/vt/topo/topoproto"
	"golang.org/x/net/context"

	topodatapb "github.com/youtube/vitess/go/vt/proto/topodata"
)

// Cleaner remembers a list of cleanup steps to perform.  Just record
// action cleanup steps, and execute them at the end in reverse
// order, with various guarantees.
type Cleaner struct {
	// following members protected by lock
	mu      sync.Mutex
	actions []cleanerActionReference
}

// cleanerActionReference is the node used by Cleaner
type cleanerActionReference struct {
	name   string
	target string
	action CleanerAction
}

// CleanerAction is the interface that clean-up actions need to implement
type CleanerAction interface {
	CleanUp(context.Context, *Wrangler) error
}

// Record will add a cleaning action to the list
func (cleaner *Cleaner) Record(name, target string, action CleanerAction) {
	cleaner.mu.Lock()
	cleaner.actions = append(cleaner.actions, cleanerActionReference{
		name:   name,
		target: target,
		action: action,
	})
	cleaner.mu.Unlock()
}

type cleanUpHelper struct {
	err error
}

// CleanUp will run the recorded actions.
// If an action on a target fails, it will not run the next action on
// the same target.
// We return the aggregate errors for all cleanups.
// CleanUp uses its own context, with a timeout of 5 minutes, so that clean up action will run even if the original context times out.
// TODO(alainjobart) Actions should run concurrently on a per target
// basis. They are then serialized on each target.
func (cleaner *Cleaner) CleanUp(wr *Wrangler) error {
	// we use a background context so we're not dependent on the original context timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	actionMap := make(map[string]*cleanUpHelper)
	rec := concurrency.AllErrorRecorder{}
	cleaner.mu.Lock()
	for i := len(cleaner.actions) - 1; i >= 0; i-- {
		actionReference := cleaner.actions[i]
		helper, ok := actionMap[actionReference.target]
		if !ok {
			helper = &cleanUpHelper{
				err: nil,
			}
			actionMap[actionReference.target] = helper
		}
		if helper.err != nil {
			wr.Logger().Warningf("previous action failed on target %v, no running %v", actionReference.target, actionReference.name)
			continue
		}
		err := actionReference.action.CleanUp(ctx, wr)
		if err != nil {
			helper.err = err
			rec.RecordError(err)
			wr.Logger().Errorf("action %v failed on %v: %v", actionReference.name, actionReference.target, err)
		} else {
			wr.Logger().Infof("action %v successful on %v", actionReference.name, actionReference.target)
		}
	}
	cleaner.mu.Unlock()
	cancel()
	return rec.Error()
}

// GetActionByName returns the first action in the list with the given
// name and target
func (cleaner *Cleaner) GetActionByName(name, target string) (CleanerAction, error) {
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	for _, action := range cleaner.actions {
		if action.name == name && action.target == target {
			return action.action, nil
		}
	}
	return nil, topo.ErrNoNode
}

// RemoveActionByName removes an action from the cleaner list
func (cleaner *Cleaner) RemoveActionByName(name, target string) error {
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	for i, action := range cleaner.actions {
		if action.name == name && action.target == target {
			newActions := make([]cleanerActionReference, 0, len(cleaner.actions)-1)
			if i > 0 {
				newActions = append(newActions, cleaner.actions[0:i]...)
			}
			if i < len(cleaner.actions)-1 {
				newActions = append(newActions, cleaner.actions[i+1:len(cleaner.actions)]...)
			}
			cleaner.actions = newActions
			return nil
		}
	}
	return topo.ErrNoNode
}

//
// ChangeSlaveTypeAction CleanerAction
//

// ChangeSlaveTypeAction will change a server type to another type
type ChangeSlaveTypeAction struct {
	TabletAlias *topodatapb.TabletAlias
	TabletType  topodatapb.TabletType
}

// ChangeSlaveTypeActionName is the name of the action to change a slave type
// (can be used to find such an action by name)
const ChangeSlaveTypeActionName = "ChangeSlaveTypeAction"

// RecordChangeSlaveTypeAction records a new ChangeSlaveTypeAction
// into the specified Cleaner
func RecordChangeSlaveTypeAction(cleaner *Cleaner, tabletAlias *topodatapb.TabletAlias, tabletType topodatapb.TabletType) {
	cleaner.Record(ChangeSlaveTypeActionName, topoproto.TabletAliasString(tabletAlias), &ChangeSlaveTypeAction{
		TabletAlias: tabletAlias,
		TabletType:  tabletType,
	})
}

// FindChangeSlaveTypeActionByTarget finds the first action for the target
func FindChangeSlaveTypeActionByTarget(cleaner *Cleaner, tabletAlias *topodatapb.TabletAlias) (*ChangeSlaveTypeAction, error) {
	action, err := cleaner.GetActionByName(ChangeSlaveTypeActionName, topoproto.TabletAliasString(tabletAlias))
	if err != nil {
		return nil, err
	}
	result, ok := action.(*ChangeSlaveTypeAction)
	if !ok {
		return nil, fmt.Errorf("Action with wrong type: %v", action)
	}
	return result, nil
}

// CleanUp is part of CleanerAction interface.
func (csta ChangeSlaveTypeAction) CleanUp(ctx context.Context, wr *Wrangler) error {
	return wr.ChangeSlaveType(ctx, csta.TabletAlias, csta.TabletType)
}

//
// TabletTagAction CleanerAction
//

// TabletTagAction will add / remove a tag to a tablet. If Value is
// empty, will remove the tag.
type TabletTagAction struct {
	TabletAlias *topodatapb.TabletAlias
	Name        string
	Value       string
}

// TabletTagActionName is the name of the Tag action
const TabletTagActionName = "TabletTagAction"

// RecordTabletTagAction records a new TabletTagAction
// into the specified Cleaner
func RecordTabletTagAction(cleaner *Cleaner, tabletAlias *topodatapb.TabletAlias, name, value string) {
	cleaner.Record(TabletTagActionName, topoproto.TabletAliasString(tabletAlias), &TabletTagAction{
		TabletAlias: tabletAlias,
		Name:        name,
		Value:       value,
	})
}

// CleanUp is part of CleanerAction interface.
func (tta TabletTagAction) CleanUp(ctx context.Context, wr *Wrangler) error {
	return wr.TopoServer().UpdateTabletFields(ctx, tta.TabletAlias, func(tablet *topodatapb.Tablet) error {
		if tablet.Tags == nil {
			tablet.Tags = make(map[string]string)
		}
		if tta.Value != "" {
			tablet.Tags[tta.Name] = tta.Value
		} else {
			delete(tablet.Tags, tta.Name)
		}
		return nil
	})
}

//
// StartSlaveAction CleanerAction
//

// StartSlaveAction will restart binlog replication on a server
type StartSlaveAction struct {
	TabletInfo *topo.TabletInfo
}

// StartSlaveActionName is the name of the slave start action
const StartSlaveActionName = "StartSlaveAction"

// RecordStartSlaveAction records a new StartSlaveAction
// into the specified Cleaner
func RecordStartSlaveAction(cleaner *Cleaner, tabletInfo *topo.TabletInfo) {
	cleaner.Record(StartSlaveActionName, topoproto.TabletAliasString(tabletInfo.Alias), &StartSlaveAction{
		TabletInfo: tabletInfo,
	})
}

// CleanUp is part of CleanerAction interface.
func (sba StartSlaveAction) CleanUp(ctx context.Context, wr *Wrangler) error {
	return wr.TabletManagerClient().StartSlave(ctx, sba.TabletInfo)
}

//
// StartBlpAction CleanerAction
//

// StartBlpAction will restart binlog replication on a server
type StartBlpAction struct {
	TabletInfo *topo.TabletInfo
}

// StartBlpActionName is the name of the action to start binlog player
const StartBlpActionName = "StartBlpAction"

// RecordStartBlpAction records a new StartBlpAction
// into the specified Cleaner
func RecordStartBlpAction(cleaner *Cleaner, tabletInfo *topo.TabletInfo) {
	cleaner.Record(StartBlpActionName, topoproto.TabletAliasString(tabletInfo.Alias), &StartBlpAction{
		TabletInfo: tabletInfo,
	})
}

// CleanUp is part of CleanerAction interface.
func (sba StartBlpAction) CleanUp(ctx context.Context, wr *Wrangler) error {
	return wr.TabletManagerClient().StartBlp(ctx, sba.TabletInfo)
}
