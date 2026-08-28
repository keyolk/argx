package argocd

import "testing"

// node builds a tree node for the flatten tests.
func node(uid, kind, name string, parents ...string) Node {
	n := Node{ResourceRef: ResourceRef{UID: uid, Kind: kind, Name: name}}
	for _, p := range parents {
		n.ParentRefs = append(n.ParentRefs, ResourceRef{UID: p})
	}
	return n
}

func TestFlattenNestsByParentRef(t *testing.T) {
	tree := &Tree{Nodes: []Node{
		node("pod-1", "Pod", "web-abc-1", "rs-1"),
		node("rs-1", "ReplicaSet", "web-abc", "deploy-1"),
		node("deploy-1", "Deployment", "web"),
	}}

	rows := tree.Flatten("argocd", "prod")
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	want := []struct {
		name  string
		depth int
	}{
		{"web", 0},
		{"web-abc", 1},
		{"web-abc-1", 2},
	}
	for i, w := range want {
		if rows[i].Node.Name != w.name || rows[i].Depth != w.depth {
			t.Errorf("row %d = %s@%d, want %s@%d",
				i, rows[i].Node.Name, rows[i].Depth, w.name, w.depth)
		}
	}
}

// A node whose parent is absent from the tree — which happens when a kind is
// excluded from tracking — must still be rendered, at the root.
func TestFlattenKeepsNodesWithMissingParent(t *testing.T) {
	tree := &Tree{Nodes: []Node{
		node("orphan", "Pod", "stray", "gone"),
		node("deploy-1", "Deployment", "web"),
	}}

	rows := tree.Flatten("argocd", "prod")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 — a node with a missing parent must not disappear", len(rows))
	}
	for _, r := range rows {
		if r.Depth != 0 {
			t.Errorf("%s depth = %d, want 0", r.Node.Name, r.Depth)
		}
	}
}

// A parentRef cycle must terminate rather than recursing until the stack blows.
func TestFlattenTerminatesOnCycle(t *testing.T) {
	tree := &Tree{Nodes: []Node{
		node("a", "Pod", "a", "b"),
		node("b", "Pod", "b", "a"),
	}}

	rows := tree.Flatten("argocd", "prod")
	if len(rows) == 0 {
		t.Fatal("a cyclic tree should still render something")
	}
	if len(rows) > 2 {
		t.Fatalf("rows = %d, want at most 2 — a cycle must not duplicate nodes", len(rows))
	}
}

func TestFlattenSortsWorkloadsFirst(t *testing.T) {
	tree := &Tree{Nodes: []Node{
		node("cm", "ConfigMap", "settings"),
		node("svc", "Service", "web"),
		node("dep", "Deployment", "web"),
	}}

	rows := tree.Flatten("argocd", "prod")
	got := []string{rows[0].Node.Kind, rows[1].Node.Kind, rows[2].Node.Kind}
	want := []string{"Deployment", "Service", "ConfigMap"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestFlattenStampsAppNamespace(t *testing.T) {
	tree := &Tree{Nodes: []Node{node("dep", "Deployment", "web")}}
	rows := tree.Flatten("team-apps", "prod")
	if got := rows[0].Node.AppNamespace; got != "team-apps" {
		t.Errorf("AppNamespace = %q, want team-apps", got)
	}
}

func TestFlattenMarksLastChild(t *testing.T) {
	tree := &Tree{Nodes: []Node{
		node("dep", "Deployment", "web"),
		node("p1", "Pod", "a", "dep"),
		node("p2", "Pod", "b", "dep"),
	}}
	rows := tree.Flatten("argocd", "prod")
	if rows[1].Last {
		t.Error("first child should not be marked last")
	}
	if !rows[2].Last {
		t.Error("final child should be marked last so the corner connector is drawn")
	}
}

func TestDegradedDetectsFailedOperation(t *testing.T) {
	var a Application
	a.Status.Health.Status = "Healthy"
	if a.Degraded() {
		t.Error("a healthy app with no operation should not be degraded")
	}

	a.Status.OperationState = &OperationState{Phase: "Failed"}
	if !a.Degraded() {
		t.Error("a failed sync operation must mark the app degraded even when health is Healthy")
	}

	a.Status.OperationState = &OperationState{Phase: "Succeeded"}
	a.Status.Conditions = []Condition{{Type: "ComparisonError", Message: "boom"}}
	if !a.Degraded() {
		t.Error("an error condition must mark the app degraded")
	}
}

func TestDestinationCluster(t *testing.T) {
	tests := []struct {
		dst  Destination
		want string
	}{
		{Destination{Name: "prod-apne2"}, "prod-apne2"},
		{Destination{Server: "https://kubernetes.default.svc"}, "in-cluster"},
		{Destination{Server: "https://eks.example.com"}, "https://eks.example.com"},
	}
	for _, tt := range tests {
		if got := tt.dst.Cluster(); got != tt.want {
			t.Errorf("Cluster() = %q, want %q", got, tt.want)
		}
	}
}

func TestPrimarySourceReportsMultiSourceCount(t *testing.T) {
	var a Application
	a.Spec.Sources = []Source{{RepoURL: "a"}, {RepoURL: "b"}}
	src, n := a.PrimarySource()
	if src.RepoURL != "a" || n != 2 {
		t.Errorf("PrimarySource() = %q,%d — want the first source and a count of 2", src.RepoURL, n)
	}

	var single Application
	single.Spec.Source = &Source{RepoURL: "only"}
	src, n = single.PrimarySource()
	if src.RepoURL != "only" || n != 1 {
		t.Errorf("PrimarySource() = %q,%d, want only,1", src.RepoURL, n)
	}

	var none Application
	if _, n := none.PrimarySource(); n != 0 {
		t.Errorf("an app with no source should report 0, got %d", n)
	}
}

func TestAutoSyncFlags(t *testing.T) {
	var a Application
	if on, _, _ := a.AutoSync(); on {
		t.Error("no sync policy should mean auto-sync off")
	}

	a.Spec.SyncPolicy = &SyncPolicy{}
	if on, _, _ := a.AutoSync(); on {
		t.Error("a sync policy without automation should mean auto-sync off")
	}

	a.Spec.SyncPolicy = &SyncPolicy{Automated: &AutomatedSync{Prune: true, SelfHeal: true}}
	on, prune, heal := a.AutoSync()
	if !on || !prune || !heal {
		t.Errorf("AutoSync() = %v,%v,%v, want all true", on, prune, heal)
	}
}
