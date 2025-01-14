// Copyright 2014, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package topotools_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/youtube/vitess/go/vt/zktopo"
	"golang.org/x/net/context"

	. "github.com/youtube/vitess/go/vt/topotools"

	topodatapb "github.com/youtube/vitess/go/vt/proto/topodata"
)

// TestCreateShard tests a few cases for CreateShard
func TestCreateShard(t *testing.T) {
	ctx := context.Background()
	cells := []string{"test_cell"}

	// Set up topology.
	ts := zktopo.NewTestServer(t, cells)

	keyspace := "test_keyspace"
	shard := "0"

	// create shard in a non-existing keyspace
	if err := CreateShard(ctx, ts, keyspace, shard); err == nil {
		t.Fatalf("CreateShard(invalid keyspace) didn't fail")
	}

	// create keyspace
	if err := ts.CreateKeyspace(ctx, keyspace, &topodatapb.Keyspace{}); err != nil {
		t.Fatalf("CreateKeyspace failed: %v", err)
	}

	// create shard should now work
	if err := CreateShard(ctx, ts, keyspace, shard); err != nil {
		t.Fatalf("CreateShard failed: %v", err)
	}
}

// TestCreateShardCustomSharding checks ServedTypes is set correctly
// when creating multiple custom sharding shards
func TestCreateShardCustomSharding(t *testing.T) {
	ctx := context.Background()
	cells := []string{"test_cell"}

	// Set up topology.
	ts := zktopo.NewTestServer(t, cells)

	// create keyspace
	keyspace := "test_keyspace"
	if err := ts.CreateKeyspace(ctx, keyspace, &topodatapb.Keyspace{}); err != nil {
		t.Fatalf("CreateKeyspace failed: %v", err)
	}

	// create first shard in keyspace
	shard0 := "0"
	if err := CreateShard(ctx, ts, keyspace, shard0); err != nil {
		t.Fatalf("CreateShard(shard0) failed: %v", err)
	}
	if si, err := ts.GetShard(ctx, keyspace, shard0); err != nil {
		t.Fatalf("GetShard(shard0) failed: %v", err)
	} else {
		if len(si.ServedTypes) != 3 {
			t.Fatalf("shard0 should have all 3 served types")
		}
	}

	// create second shard in keyspace
	shard1 := "1"
	if err := CreateShard(ctx, ts, keyspace, shard1); err != nil {
		t.Fatalf("CreateShard(shard1) failed: %v", err)
	}
	if si, err := ts.GetShard(ctx, keyspace, shard1); err != nil {
		t.Fatalf("GetShard(shard1) failed: %v", err)
	} else {
		if len(si.ServedTypes) != 3 {
			t.Fatalf("shard1 should have all 3 served types")
		}
	}
}

// TestGetOrCreateShard will create / get 100 shards in a keyspace
// for a long time in parallel, making sure the locking and everything
// works correctly.
func TestGetOrCreateShard(t *testing.T) {
	ctx := context.Background()
	cells := []string{"test_cell"}

	// Set up topology.
	ts := zktopo.NewTestServer(t, cells)

	// and do massive parallel GetOrCreateShard
	keyspace := "test_keyspace"
	wg := sync.WaitGroup{}
	rand.Seed(time.Now().UnixNano())
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			for j := 0; j < 100; j++ {
				index := rand.Intn(10)
				shard := fmt.Sprintf("%v", index)
				si, err := GetOrCreateShard(ctx, ts, keyspace, shard)
				if err != nil {
					t.Errorf("GetOrCreateShard(%v, %v) failed: %v", i, shard, err)
				}
				if si.ShardName() != shard {
					t.Errorf("si.ShardName() is wrong, got %v expected %v", si.ShardName(), shard)
				}
			}
		}(i)
	}
	wg.Wait()
}
