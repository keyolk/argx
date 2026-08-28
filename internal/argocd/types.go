package argocd

import (
	"sort"
	"strings"
	"time"
)

// Application is the subset of the Argo CD Application CR that argx renders.
type Application struct {
	// Context is the argx context this application was fetched from. It is not
	// part of the API payload — the fleet stamps it on arrival so every later
	// action can resolve the right server rather than assuming a current one.
	Context string `json:"-"`

	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Labels            map[string]string `json:"labels"`
		Annotations       map[string]string `json:"annotations"`
		CreationTimestamp time.Time         `json:"creationTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Project     string      `json:"project"`
		Source      *Source     `json:"source"`
		Sources     []Source    `json:"sources"`
		Destination Destination `json:"destination"`
		SyncPolicy  *SyncPolicy `json:"syncPolicy"`
	} `json:"spec"`
	Status struct {
		Sync struct {
			Status   string `json:"status"`
			Revision string `json:"revision"`
		} `json:"sync"`
		Health         Health            `json:"health"`
		OperationState *OperationState   `json:"operationState"`
		Conditions     []Condition       `json:"conditions"`
		ReconciledAt   time.Time         `json:"reconciledAt"`
		History        []RevisionHistory `json:"history"`
	} `json:"status"`
}

// RevisionHistory is one past deployment of the application. Argo CD keeps a
// bounded window of these, and their IDs are what a rollback targets.
type RevisionHistory struct {
	ID              int64     `json:"id"`
	Revision        string    `json:"revision"`
	Revisions       []string  `json:"revisions"`
	Source          *Source   `json:"source"`
	Sources         []Source  `json:"sources"`
	DeployedAt      time.Time `json:"deployedAt"`
	DeployStartedAt time.Time `json:"deployStartedAt"`
	InitiatedBy     struct {
		Username  string `json:"username"`
		Automated bool   `json:"automated"`
	} `json:"initiatedBy"`
}

// Who renders who triggered the deployment.
func (h RevisionHistory) Who() string {
	if h.InitiatedBy.Automated {
		return "auto-sync"
	}
	if h.InitiatedBy.Username != "" {
		return h.InitiatedBy.Username
	}
	return "unknown"
}

// Rev is the revision this history entry deployed, preferring the per-source
// list for multi-source applications.
func (h RevisionHistory) Rev() string {
	if h.Revision != "" {
		return h.Revision
	}
	if len(h.Revisions) > 0 {
		return h.Revisions[0]
	}
	return ""
}

// SyncPolicy is the application's configured sync automation.
type SyncPolicy struct {
	Automated *AutomatedSync `json:"automated"`
}

// AutomatedSync is the automated sync policy's flags.
type AutomatedSync struct {
	Prune    bool `json:"prune"`
	SelfHeal bool `json:"selfHeal"`
}

// OperationState is the outcome of the most recent sync operation.
type OperationState struct {
	Phase      string    `json:"phase"`
	Message    string    `json:"message"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

// Condition is an application-level condition, e.g. ComparisonError.
type Condition struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Health is a health status with an optional explanation.
type Health struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Source is one of an application's manifest sources.
type Source struct {
	RepoURL        string `json:"repoURL"`
	Path           string `json:"path"`
	TargetRevision string `json:"targetRevision"`
	Chart          string `json:"chart"`
}

// Destination is where an application deploys to.
type Destination struct {
	Server    string `json:"server"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// Cluster renders the destination cluster in whichever form the app declares.
func (d Destination) Cluster() string {
	if d.Name != "" {
		return d.Name
	}
	if d.Server == "https://kubernetes.default.svc" {
		return "in-cluster"
	}
	return d.Server
}

// AppNamespace is the namespace the Application CR itself lives in, needed as a
// query parameter for apps outside the control-plane namespace.
func (a *Application) AppNamespace() string { return a.Metadata.Namespace }

// Name is the application's name.
func (a *Application) Name() string { return a.Metadata.Name }

// AutoSync reports whether an automated sync policy is configured, and whether
// it prunes and self-heals.
func (a *Application) AutoSync() (on, prune, selfHeal bool) {
	if a.Spec.SyncPolicy == nil || a.Spec.SyncPolicy.Automated == nil {
		return false, false, false
	}
	au := a.Spec.SyncPolicy.Automated
	return true, au.Prune, au.SelfHeal
}

// PrimarySource returns the source to display. Multi-source apps show their
// first source; the count is surfaced separately so the display never implies
// there is only one.
func (a *Application) PrimarySource() (Source, int) {
	if len(a.Spec.Sources) > 0 {
		return a.Spec.Sources[0], len(a.Spec.Sources)
	}
	if a.Spec.Source != nil {
		return *a.Spec.Source, 1
	}
	return Source{}, 0
}

// TargetRevision is the revision the application tracks. For a multi-source
// application this is the first source's — editing beyond the first source is
// not offered, because there is no single field to edit.
func (a *Application) TargetRevision() string {
	src, _ := a.PrimarySource()
	return src.TargetRevision
}

// SingleSource reports whether the application has exactly one source, which is
// the only shape whose target revision argx will edit.
func (a *Application) SingleSource() bool {
	_, n := a.PrimarySource()
	return n == 1
}

// RepoURL is the source repository, used to look up branches and tags.
func (a *Application) RepoURL() string {
	src, _ := a.PrimarySource()
	return src.RepoURL
}

// Degraded reports whether the app is in a state a human should look at:
// unhealthy, failed sync operation, or an error condition.
func (a *Application) Degraded() bool {
	switch a.Status.Health.Status {
	case "Degraded", "Missing", "Unknown":
		return true
	}
	if op := a.Status.OperationState; op != nil {
		switch op.Phase {
		case "Failed", "Error":
			return true
		}
	}
	for _, c := range a.Status.Conditions {
		if strings.Contains(strings.ToLower(c.Type), "error") {
			return true
		}
	}
	return false
}

// ResourceRef identifies one Kubernetes resource inside an application.
type ResourceRef struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`

	// AppNamespace is not part of the API payload; argx threads it through so
	// resource requests can scope themselves for apps outside the control-plane
	// namespace.
	AppNamespace string `json:"-"`

	// Context is the argx context the owning application came from, carried for
	// the same reason as Application.Context.
	Context string `json:"-"`
}

// GroupKind renders the resource's kind with its API group, as `kubectl` would.
func (r ResourceRef) GroupKind() string {
	if r.Group == "" {
		return r.Kind
	}
	return r.Kind + "." + r.Group
}

// Tree is the live resource tree of an application.
type Tree struct {
	Nodes []Node `json:"nodes"`
	// OrphanedNodes are resources in the destination namespace that the app
	// does not manage. Argo CD only populates this when the project enables
	// orphaned-resource monitoring.
	OrphanedNodes []Node `json:"orphanedNodes"`
}

// Node is one resource in the tree.
type Node struct {
	ResourceRef
	ParentRefs      []ResourceRef   `json:"parentRefs"`
	Health          *Health         `json:"health"`
	Info            []InfoItem      `json:"info"`
	NetworkingInfo  *NetworkingInfo `json:"networkingInfo"`
	CreatedAt       *time.Time      `json:"createdAt"`
	ResourceVersion string          `json:"resourceVersion"`
	Images          []string        `json:"images"`
}

// NetworkingInfo is the networking detail Argo CD attaches to some nodes. It is
// the only place a resource tree carries labels, and only for the kinds Argo CD
// tracks networking for — a Pod has them, a ConfigMap does not.
type NetworkingInfo struct {
	Labels       map[string]string `json:"labels"`
	TargetLabels map[string]string `json:"targetLabels"`
}

// Labels are the resource's labels, or nil when Argo CD reports none for this
// kind. Nil is a real answer here, not missing data: the tree API simply does
// not carry labels for most kinds.
func (n Node) Labels() map[string]string {
	if n.NetworkingInfo == nil {
		return nil
	}
	return n.NetworkingInfo.Labels
}

// InfoItem is one of the key/value facts Argo CD attaches to a tree node.
type InfoItem struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HealthStatus is the node's health, or "" when Argo CD reports none (which is
// normal for kinds without a health check, e.g. ConfigMap).
func (n Node) HealthStatus() string {
	if n.Health == nil {
		return ""
	}
	return n.Health.Status
}

// InfoValue returns the named info entry, e.g. "Status Reason" or "Revision".
func (n Node) InfoValue(name string) string {
	for _, i := range n.Info {
		if i.Name == name {
			return i.Value
		}
	}
	return ""
}

// IsPod reports whether the node is a core Pod, which is what enables the log
// view.
func (n Node) IsPod() bool { return n.Group == "" && n.Kind == "Pod" }

// TreeRow is a flattened tree node carrying its indent depth, ready to render
// as one line.
type TreeRow struct {
	Node  Node
	Depth int
	// Last marks the final child of its parent so the renderer can pick the
	// corner connector.
	Last bool
}

// Flatten turns the tree's parent links into an ordered, indented row list.
//
// Argo CD returns nodes as a flat list with parentRefs rather than a nested
// structure, and a node's parent may legitimately be absent from the tree (for
// instance when the parent kind is excluded); such nodes are rendered at the
// root so nothing silently disappears.
func (t *Tree) Flatten(appNamespace, ctxName string) []TreeRow {
	byUID := make(map[string]Node, len(t.Nodes))
	for _, n := range t.Nodes {
		byUID[n.UID] = n
	}

	children := make(map[string][]Node, len(t.Nodes))
	var roots []Node
	for _, n := range t.Nodes {
		parent := ""
		for _, p := range n.ParentRefs {
			if _, ok := byUID[p.UID]; ok {
				parent = p.UID
				break
			}
		}
		if parent == "" {
			roots = append(roots, n)
			continue
		}
		children[parent] = append(children[parent], n)
	}

	sortNodes := func(ns []Node) {
		sort.Slice(ns, func(i, j int) bool {
			if ns[i].Kind != ns[j].Kind {
				return kindRank(ns[i].Kind) < kindRank(ns[j].Kind)
			}
			return ns[i].Name < ns[j].Name
		})
	}
	sortNodes(roots)
	for k := range children {
		sortNodes(children[k])
	}

	var rows []TreeRow
	// Explicit stack rather than recursion: a cyclic parentRef would otherwise
	// blow the stack, and the visited set here makes the cycle terminate.
	seen := make(map[string]bool, len(t.Nodes))
	var walk func(n Node, depth int, last bool)
	walk = func(n Node, depth int, last bool) {
		if seen[n.UID] {
			return
		}
		seen[n.UID] = true
		n.AppNamespace = appNamespace
		n.Context = ctxName
		rows = append(rows, TreeRow{Node: n, Depth: depth, Last: last})
		kids := children[n.UID]
		for i, c := range kids {
			walk(c, depth+1, i == len(kids)-1)
		}
	}
	for i, r := range roots {
		walk(r, 0, i == len(roots)-1)
	}

	// A parentRef cycle leaves every node in it claiming a parent, so none of
	// them became a root and the whole cycle would vanish from the view. Adopt
	// whatever is left over at the root rather than silently hiding resources.
	var orphans []Node
	for _, n := range t.Nodes {
		if !seen[n.UID] {
			orphans = append(orphans, n)
		}
	}
	sortNodes(orphans)
	for i, n := range orphans {
		walk(n, 0, i == len(orphans)-1)
	}
	return rows
}

// kindRank orders kinds so that the workload controllers a human looks for
// first sort above the plumbing.
func kindRank(kind string) int {
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet", "Rollout", "CronJob", "Job":
		return 0
	case "ReplicaSet":
		return 1
	case "Pod":
		return 2
	case "Service", "Ingress", "VirtualService", "Gateway":
		return 3
	case "ConfigMap", "Secret":
		return 5
	default:
		return 4
	}
}

// ResourceDiff is one entry of the managed-resources response: the live object
// and the desired object, plus whether Argo CD considers them different.
type ResourceDiff struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// TargetState is the desired manifest as JSON, empty when the resource
	// exists live but is not in the desired state (a prune candidate).
	TargetState string `json:"targetState"`
	// LiveState is the live manifest as JSON, empty when the resource is
	// missing from the cluster.
	LiveState string `json:"liveState"`
	Diff      string `json:"diff"`
	Hook      bool   `json:"hook"`
	// NormalizedLiveState has Argo CD's diff normalizations applied and is what
	// the server itself compares, so it is the honest input for a diff view.
	NormalizedLiveState string `json:"normalizedLiveState"`
	PredictedLiveState  string `json:"predictedLiveState"`
	Modified            bool   `json:"modified"`
}

// Ref identifies the diffed resource.
func (d ResourceDiff) Ref() ResourceRef {
	return ResourceRef{Group: d.Group, Kind: d.Kind, Namespace: d.Namespace, Name: d.Name}
}

// Event is a Kubernetes event associated with an application.
type Event struct {
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	Type           string `json:"type"`
	Count          int32  `json:"count"`
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"involvedObject"`
	FirstTimestamp time.Time `json:"firstTimestamp"`
	LastTimestamp  time.Time `json:"lastTimestamp"`
}
