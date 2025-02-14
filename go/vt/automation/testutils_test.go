// Copyright 2015, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package automation

import (
	"errors"
	"testing"

	automationpb "github.com/youtube/vitess/go/vt/proto/automation"
)

func testingTaskCreator(taskName string) Task {
	switch taskName {
	// Tasks for testing only.
	case "TestingEchoTask":
		return &TestingEchoTask{}
	case "TestingFailTask":
		return &TestingFailTask{}
	case "TestingEmitEchoTask":
		return &TestingEmitEchoTask{}
	case "TestingEmitEchoFailEchoTask":
		return &TestingEmitEchoFailEchoTask{}
	default:
		return nil
	}
}

// TestingEchoTask is used only for testing. It returns the join of all parameter values.
type TestingEchoTask struct {
}

func (t *TestingEchoTask) Run(parameters map[string]string) (newTasks []*automationpb.TaskContainer, output string, err error) {
	for _, v := range parameters {
		output += v
	}
	return
}

func (t *TestingEchoTask) RequiredParameters() []string {
	return []string{"echo_text"}
}

func (t *TestingEchoTask) OptionalParameters() []string {
	return nil
}

// TestingFailTask is used only for testing. It always fails.
type TestingFailTask struct {
}

func (t *TestingFailTask) Run(parameters map[string]string) (newTasks []*automationpb.TaskContainer, output string, err error) {
	return nil, "something went wrong", errors.New("full error message")
}

func (t *TestingFailTask) RequiredParameters() []string {
	return []string{"echo_text"}
}

func (t *TestingFailTask) OptionalParameters() []string {
	return nil
}

// TestingEmitEchoTask is used only for testing. It emits a TestingEchoTask.
type TestingEmitEchoTask struct {
}

func (t *TestingEmitEchoTask) Run(parameters map[string]string) (newTasks []*automationpb.TaskContainer, output string, err error) {
	return []*automationpb.TaskContainer{
		NewTaskContainerWithSingleTask("TestingEchoTask", parameters),
	}, "emitted TestingEchoTask", nil
}

func (t *TestingEmitEchoTask) RequiredParameters() []string {
	return []string{"echo_text"}
}

func (t *TestingEmitEchoTask) OptionalParameters() []string {
	return nil
}

// TestingEmitEchoFailEchoTask is used only for testing.
// It emits three sequential tasks: Echo, Fail, Echo.
type TestingEmitEchoFailEchoTask struct {
}

func (t *TestingEmitEchoFailEchoTask) Run(parameters map[string]string) (newTasks []*automationpb.TaskContainer, output string, err error) {
	newTasks = []*automationpb.TaskContainer{
		NewTaskContainerWithSingleTask("TestingEchoTask", parameters),
		NewTaskContainerWithSingleTask("TestingFailTask", parameters),
		NewTaskContainerWithSingleTask("TestingEchoTask", parameters),
	}
	return newTasks, "emitted tasks: Echo, Fail, Echo", nil
}

func (t *TestingEmitEchoFailEchoTask) RequiredParameters() []string {
	return []string{"echo_text"}
}

func (t *TestingEmitEchoFailEchoTask) OptionalParameters() []string {
	return nil
}

// testTask runs the given tasks and checks if it succeeds.
// To make the task succeed you have to register the result with a fake first
// e.g. see migrate_served_types_task_test.go for an example.
func testTask(t *testing.T, test string, task Task, parameters map[string]string) {
	err := validateParameters(task, parameters)
	if err != nil {
		t.Fatalf("%s: Not all required parameters were specified: %v", test, err)
	}

	newTasks, _ /* output */, err := task.Run(parameters)
	if newTasks != nil {
		t.Errorf("%s: Task should not emit new tasks: %v", test, newTasks)
	}
	if err != nil {
		t.Errorf("%s: Task should not fail: %v", err, test)
	}
}
