package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLsFilesSnapshotIsRelativeToHome(t *testing.T) {
	e, _, log := webSetup(t)
	res, err := Ls(context.Background(), e, LsRequest{Subscription: "example.test", Snapshot: "aaaaaaaa", Path: "httpdocs"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != home(e)+"/httpdocs" || len(res.Entries) != 1 || res.Entries[0].Name != "index.html" ||
		res.Entries[0].Type != "file" || res.Entries[0].Size != 6 || res.Entries[0].MTime.IsZero() || res.Truncated {
		t.Errorf("%+v", res)
	}
	if !strings.Contains(log(), "ls --json "+filesSnap+" "+home(e)+"/httpdocs") {
		t.Errorf("%s", log())
	}
}

func TestLsDBAndServer(t *testing.T) {
	e, _, _ := webSetup(t)
	res, err := Ls(context.Background(), e, LsRequest{Subscription: "example.test", Snapshot: "bbbbbbbb"})
	if err != nil || res.Path != "/" {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = Ls(context.Background(), e, LsRequest{Subscription: "_server", Snapshot: "cccccccc"})
	if err != nil || !strings.HasSuffix(res.Path, "/staging/panel-config-20261022T020000Z.tar") {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := Ls(context.Background(), e, LsRequest{Subscription: "_server", Snapshot: "cccccccc", Path: "x"}); err == nil {
		t.Error("_server ls took a path")
	}
}

func TestLsCapsEntries(t *testing.T) {
	e, env, _ := webSetup(t)
	var b strings.Builder
	fmt.Fprintf(&b, `{"name":"example.test","type":"dir","path":"%s","message_type":"node"}`+"\n", home(e))
	for i := 0; i < MaxLsEntries+10; i++ {
		fmt.Fprintf(&b, `{"name":"f%d","type":"file","path":"%s/f%d","size":1,"message_type":"node"}`+"\n", i, home(e), i)
	}
	out := filepath.Join(t.TempDir(), "ls.out")
	os.WriteFile(out, []byte(b.String()), 0o600)
	env["FAKE_LS_OUT"] = out
	res, err := Ls(context.Background(), e, LsRequest{Subscription: "example.test", Snapshot: "aaaaaaaa"})
	if err != nil || len(res.Entries) != MaxLsEntries || !res.Truncated || res.Entries[0].Name != "f0" {
		t.Fatalf("%d %v %v", len(res.Entries), res.Truncated, err)
	}
}

func TestLsRejectsBadInput(t *testing.T) {
	e, env, _ := webSetup(t)
	for _, r := range []LsRequest{
		{Subscription: "example.test", Snapshot: "latest"},
		{Subscription: "example.test", Snapshot: "aaaaaaaa", Path: "../x"},
		{Subscription: "example.test", Snapshot: "aaaaaaaa", Path: "/etc"},
		{Subscription: "nope.example.test", Snapshot: "aaaaaaaa"},
		{Subscription: "example.test", Snapshot: "99999999"},
	} {
		if _, err := Ls(context.Background(), e, r); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
	env["FAKE_LS_EMPTY"] = "1"
	if _, err := Ls(context.Background(), e, LsRequest{Subscription: "example.test", Snapshot: "aaaaaaaa", Path: "nope"}); err == nil || !strings.Contains(err.Error(), "not in snapshot") {
		t.Errorf("missing path: %v", err)
	}
}
