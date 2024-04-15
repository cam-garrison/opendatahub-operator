package labels


const (
	ODHAppPrefix      = "app.opendatahub.io"
  ODHSecurityPrefix = "security.opendatahub.io"
	InjectTrustCA     = "config.openshift.io/inject-trusted-cabundle"
	SecurityEnforce   = "pod-security.kubernetes.io/enforce"
	ClusterMonitoring = "openshift.io/cluster-monitoring"
)

// K8SCommon keeps common kubernetes labels [1]
// used across the project.
// [1] (https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/#labels)
var K8SCommon = struct {
	PartOf string
}{
	PartOf: "app.kubernetes.io/part-of",
}

// ODH holds Open Data Hub specific labels grouped by types.
var ODH = struct {
	OwnedNamespace     string
	Component          func(string) string
	AuthorizationGroup func(string) string
}{
	OwnedNamespace: "opendatahub.io/generated-namespace",
	Component: func(name string) string {
		return ODHAppPrefix + "/" + name
	},
	AuthorizationGroup: func(group string) string { return ODHSecurityPrefix + "/authorization-group=" + group },
}
