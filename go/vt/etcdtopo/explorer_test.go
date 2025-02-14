// Copyright 2014, Google Inc. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package etcdtopo

import (
	"encoding/json"
	"path"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/context"

	topodatapb "github.com/youtube/vitess/go/vt/proto/topodata"
)

func toJSON(t *testing.T, value interface{}) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("cannot JSON encode: %v", err)
	}
	return string(data)
}

func TestSplitCellPath(t *testing.T) {
	table := map[string][]string{
		"/cell-a":            []string{"cell-a", "/"},
		"/cell-b/x":          []string{"cell-b", "/x"},
		"/cell1/other/stuff": []string{"cell1", "/other/stuff"},
	}
	for input, want := range table {
		cell, rest, err := splitCellPath(input)
		if err != nil {
			t.Errorf("splitCellPath error: %v", err)
		}
		if cell != want[0] || rest != want[1] {
			t.Errorf("splitCellPath(%q) = (%q, %q), want (%q, %q)",
				input, cell, rest, want[0], want[1])
		}
	}
}

func TestSplitShardDirPath(t *testing.T) {
	// Make sure keyspace/shard names are preserved through a "round-trip".
	input := shardDirPath("my-keyspace", "my-shard")
	keyspace, shard, err := splitShardDirPath(input)
	if err != nil {
		t.Errorf("splitShardDirPath error: %v", err)
	}
	if keyspace != "my-keyspace" || shard != "my-shard" {
		t.Errorf("splitShardDirPath(%q) = (%q, %q), want (%q, %q)",
			input, keyspace, shard, "my-keyspace", "my-shard")
	}
}

func TestHandlePathInvalid(t *testing.T) {
	// Don't panic!
	ex := NewExplorer(nil)
	result := ex.HandlePath("xxx", nil)
	exResult := result.(*explorerResult)
	if want := "invalid"; !strings.Contains(exResult.Error, want) {
		t.Errorf("HandlePath returned wrong error: got %q, want %q", exResult.Error, want)
	}
}

func TestHandlePathRoot(t *testing.T) {
	input := explorerRoot
	cells := []string{"cell1", "cell2", "cell3"}
	want := []string{"global", "cell1", "cell2", "cell3"}

	ts := newTestServer(t, cells)
	ex := NewExplorer(ts)
	result := ex.HandlePath(input, nil)
	exResult := result.(*explorerResult)
	if got := exResult.Children; !reflect.DeepEqual(got, want) {
		t.Errorf("HandlePath(%q) = %v, want %v", input, got, want)
	}
}

func TestHandlePathKeyspace(t *testing.T) {
	input := path.Join(explorerRoot, "global", keyspaceDirPath("test_keyspace"))
	cells := []string{"cell1", "cell2", "cell3"}
	keyspace := &topodatapb.Keyspace{}
	shard := &topodatapb.Shard{}
	want := toJSON(t, keyspace)

	ctx := context.Background()
	ts := newTestServer(t, cells)
	if err := ts.CreateKeyspace(ctx, "test_keyspace", keyspace); err != nil {
		t.Fatalf("CreateKeyspace error: %v", err)
	}
	if err := ts.CreateShard(ctx, "test_keyspace", "10-20", shard); err != nil {
		t.Fatalf("CreateShard error: %v", err)
	}
	if err := ts.CreateShard(ctx, "test_keyspace", "20-30", shard); err != nil {
		t.Fatalf("CreateShard error: %v", err)
	}

	ex := NewExplorer(ts)
	result := ex.HandlePath(input, nil)
	exResult := result.(*explorerResult)
	if got := exResult.Data; got != want {
		t.Errorf("HandlePath(%q) = %q, want %q", input, got, want)
	}
	if got, want := exResult.Children, []string{"10-20", "20-30"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Children = %v, want %v", got, want)
	}
}

func TestHandlePathShard(t *testing.T) {
	input := path.Join(explorerRoot, "global", shardDirPath("test_keyspace", "-80"))
	cells := []string{"cell1", "cell2", "cell3"}
	keyspace := &topodatapb.Keyspace{}
	shard := &topodatapb.Shard{}
	want := toJSON(t, shard)

	ctx := context.Background()
	ts := newTestServer(t, cells)
	if err := ts.CreateKeyspace(ctx, "test_keyspace", keyspace); err != nil {
		t.Fatalf("CreateKeyspace error: %v", err)
	}
	if err := ts.CreateShard(ctx, "test_keyspace", "-80", shard); err != nil {
		t.Fatalf("CreateShard error: %v", err)
	}

	ex := NewExplorer(ts)
	result := ex.HandlePath(input, nil)
	exResult := result.(*explorerResult)
	if got := exResult.Data; got != want {
		t.Errorf("HandlePath(%q) = %q, want %q", input, got, want)
	}
}

func TestHandlePathTablet(t *testing.T) {
	input := path.Join(explorerRoot, "cell1", path.Join(tabletsDirPath, "cell1-0000000123"))
	cells := []string{"cell1", "cell2", "cell3"}
	tablet := &topodatapb.Tablet{
		Alias:    &topodatapb.TabletAlias{Cell: "cell1", Uid: 123},
		Hostname: "example.com",
		PortMap:  map[string]int32{"vt": 4321},
	}
	want := toJSON(t, tablet)

	ctx := context.Background()
	ts := newTestServer(t, cells)
	if err := ts.CreateTablet(ctx, tablet); err != nil {
		t.Fatalf("CreateTablet error: %v", err)
	}

	ex := NewExplorer(ts)
	result := ex.HandlePath(input, nil)
	exResult := result.(*explorerResult)
	if got := exResult.Data; got != want {
		t.Errorf("HandlePath(%q) = %q, want %q", input, got, want)
	}
}
