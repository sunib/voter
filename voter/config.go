package main

import (
	"github.com/kelseyhightower/envconfig"
)

// Configuration for the application.
//
// Voter has no login of its own: krm-foyer, on the same host, signs people in
// and vouches for them (foyer_identity.go). So there is nothing here about
// issuers, clients, cookies or origins -- only names of things, and where Voter's
// own Kubernetes credential comes from.
type config struct {
	// Voter's own credential outside a cluster, for local development; never
	// discover the user's default kubeconfig. The name is from when it was the
	// stream gateway's.
	StreamKubeconfig string `envconfig:"STREAM_KUBECONFIG"`

	Host string `envconfig:"HOST" default:"0.0.0.0"`
	Port string `envconfig:"PORT" default:"8080"`

	CoffeeConfigName string `envconfig:"COFFEE_CONFIG_NAME" default:"testnet-coffee"`

	// RoomName is the Room whose rotating join code the operator page renders as
	// a QR. Naming it here rather than taking it from the URL keeps the stream
	// pinned to one object: a reader who can see rooms still cannot point this
	// endpoint at somebody else's.
	RoomName string `envconfig:"ROOM_NAME" default:"demo"`

	// AudienceCoffeeAdminRole names BOTH the Role that lets the audience edit
	// the coffee menu and the RoleBinding the operator page creates to hand it
	// out. One name for both, because there is exactly one of each and two
	// names would be two things to keep in step.
	//
	// The Role lives in Git and is reconciled. The RoleBinding is created and
	// deleted at runtime by operator_audience_grant.go and is deliberately NOT
	// in the Flux kustomization -- Flux would recreate whatever the switch
	// deletes.
	AudienceCoffeeAdminRole string `envconfig:"AUDIENCE_COFFEE_ADMIN_ROLE" default:"voter-audience-coffee-admin"`

	// AudienceBallotRole names the Role that lets a group cast ballots. Like the
	// coffee grant, the Role lives in Git and its RoleBindings do not: the
	// operator page binds it to the whole room or to one answer's group of the
	// Room's question, live, and names each binding after the Role and the
	// group.
	AudienceBallotRole string `envconfig:"AUDIENCE_BALLOT_ROLE" default:"voter-audience-ballot"`

	// ConfigButlerGitTargetName names the ConfigButler GitTarget whose open
	// commit window should be finalized after a successful CoffeeConfig patch.
	// Set to "" to disable the save-message side effect entirely.
	ConfigButlerGitTargetName string `envconfig:"CONFIGBUTLER_GIT_TARGET_NAME" default:"voter-demo"`
	// ConfigButlerDatabaseGitTargetName is the same thing for the Database
	// editor, and is EMPTY on purpose: no GitTarget watches
	// platform.configbutler.ai/databases yet, so there is no window to close and
	// a database save creates no CommitRequest at all.
	//
	// It is a separate setting rather than a reuse of the one above because
	// reusing it would finalize the COFFEE window from a page that did not
	// touch the coffee menu -- committing demo 1's half-finished edit under
	// somebody else's message, mid-demo. When the GitTarget exists, setting
	// this is the only change needed.
	ConfigButlerDatabaseGitTargetName string `envconfig:"CONFIGBUTLER_DATABASE_GIT_TARGET_NAME"`
	// AuditTrailCommitURLTemplate turns a commit sha into a link a participant
	// can open. "{sha}" is substituted; anything else is passed through.
	//
	// It is configuration rather than something discovered from the cluster on
	// purpose. The repository is named in a GitProvider the application has no
	// grant to read, and asking for one -- to render a hyperlink -- would be
	// spending real permission on decoration. This is one string in the
	// Deployment, and empty means the sha renders as text.
	AuditTrailCommitURLTemplate string `envconfig:"AUDIT_TRAIL_COMMIT_URL_TEMPLATE"`
	// ConfigButlerCommitRequestNamespace overrides where CommitRequest objects
	// are created. Empty means "use the runtime namespace", which must also be
	// the GitTarget's namespace.
	ConfigButlerCommitRequestNamespace string `envconfig:"CONFIGBUTLER_COMMITREQUEST_NAMESPACE"`
	// ConfigButlerCloseDelaySeconds is how long the branch worker may wait for
	// the CoffeeConfig write to reach it before finalizing the window. It is a
	// deadline, not a sleep: an attached window still closes on the first normal
	// flush, so a larger value costs nothing when the write arrives promptly.
	//
	// It must not be zero. The patch and the CommitRequest are two independent
	// trips through the API server, and the patch is the slower one -- it is held
	// head-of-line in the reverser's watch goroutine until the API server's audit
	// batch names its author. A zero delay finalizes before that lands, so the
	// request expires as NoWindowInGrace and the save commits later, under the
	// target's default message instead of the editor's. Two seconds covers the
	// audit batch (audit-webhook-batch-max-wait=1s) with margin.
	ConfigButlerCloseDelaySeconds int32 `envconfig:"CONFIGBUTLER_CLOSE_DELAY_SECONDS" default:"2"`

	// ParticipantConnectorID is the Dex connector whose logins are the
	// audience, published in /config.json so the vote screen can explain who
	// may vote. Admission holds the rule itself (config/admission/), on the
	// `demo:` username the API server derives from the same connector.
	ParticipantConnectorID string `envconfig:"PARTICIPANT_CONNECTOR_ID" default:"room-pass"`

	// KubernetesAPIServer overrides the API server Voter's own client talks to.
	// Empty means the in-cluster address, or the explicit kubeconfig's.
	KubernetesAPIServer string `envconfig:"KUBERNETES_API_SERVER"`

	// KubernetesNamespace overrides the pod namespace for application resources.
	KubernetesNamespace string `envconfig:"KUBERNETES_NAMESPACE"`

	// StaticDir is the built frontend bundle served by this process. The
	// container image sets it to /srv/www; leaving it empty (the local Vite
	// setup) serves a plain-text API description at / instead.
	StaticDir string `envconfig:"STATIC_DIR"`
}

func loadConfig() (config, error) {
	var cfg config
	if err := envconfig.Process("", &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
