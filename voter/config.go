package main

import (
	"github.com/kelseyhightower/envconfig"
	"k8s.io/client-go/rest"
)

// Configuration for the application.
//
// There is ONE authentication model: the backend is a confidential OIDC client
// of Dex, and each participant's own ID token is the credential used against
// the Kubernetes API. The settings that used to select a second model -- a
// browser-asserted identity plus ServiceAccount impersonation, with its own
// join codes, admin password and forward-auth ServiceAccount -- are gone along
// with the code behind them.
type config struct {
	// Metrics has its own listener and is never routed by the application ingress.
	MetricsAddress string `envconfig:"METRICS_ADDRESS" default:"127.0.0.1:9090"`
	// An explicit local development credential; never discover the user's default kubeconfig.
	StreamKubeconfig string `envconfig:"STREAM_KUBECONFIG"`
	// Public cluster TLS trust copied at startup, without shared-client credentials.
	participantTLS *rest.TLSClientConfig

	Host string `envconfig:"HOST" default:"0.0.0.0"`
	Port string `envconfig:"PORT" default:"8080"`

	// ParticipantCookieName holds the encrypted application session. __Host- so
	// it is host-only and cannot be set by a sibling subdomain.
	ParticipantCookieName string `envconfig:"PARTICIPANT_COOKIE_NAME" default:"__Host-voter-session"`
	// SessionCookieMaxAgeSecs caps the application session. The session ends at
	// the EARLIER of this and the ID token's own expiry -- an intact cookie must
	// never extend the authority of an expired token. The authproxy connector
	// issues no refresh token, so this is a ceiling, not a sliding window.
	SessionCookieMaxAgeSecs int `envconfig:"SESSION_COOKIE_MAX_AGE_SECONDS" default:"43200"`

	CoffeeConfigName string `envconfig:"COFFEE_CONFIG_NAME" default:"testnet-coffee"`

	// RoomName is the Room whose rotating join code the operator page renders as
	// a QR. Naming it here rather than taking it from the URL keeps the stream
	// pinned to one object: a reader who can see rooms still cannot point this
	// endpoint at somebody else's.
	RoomName string `envconfig:"ROOM_NAME" default:"demo"`

	// ConfigButlerGitTargetName names the ConfigButler GitTarget whose open
	// commit window should be finalized after a successful CoffeeConfig patch.
	// Set to "" to disable the save-message side effect entirely.
	ConfigButlerGitTargetName string `envconfig:"CONFIGBUTLER_GIT_TARGET_NAME" default:"voter-demo"`
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

	// --- OIDC ---------------------------------------------------------------
	OIDCIssuerURL    string `envconfig:"OIDC_ISSUER_URL"`
	OIDCClientID     string `envconfig:"OIDC_CLIENT_ID"`
	OIDCClientSecret string `envconfig:"OIDC_CLIENT_SECRET"`
	// OIDCRedirectURL must match a redirect URI registered on the Dex client
	// exactly. It is configuration, never derived from a request header.
	OIDCRedirectURL string `envconfig:"OIDC_REDIRECT_URL"`

	// OIDCConnectorID preselects a Dex connector, skipping the "how do you want
	// to sign in" screen Dex shows when an authorization request names none and
	// more than one is configured. For this demo it is "room-pass": the audience
	// should land on the join form, not on a choice they cannot evaluate.
	// Empty means "let Dex ask".
	OIDCConnectorID string `envconfig:"OIDC_CONNECTOR_ID"`
	// OIDCConnectorChoices are the connectors a caller may request explicitly
	// via /auth/login?connector=<id>, so an operator can still sign in with
	// GitHub on a deployment that defaults to the room connector. An allowlist
	// rather than free text: the value goes into a redirect to the issuer.
	OIDCConnectorChoices []string `envconfig:"OIDC_CONNECTOR_CHOICES"`
	// ParticipantConnectorID is the Dex connector id a ballot must come through.
	// Dex reports it in federated_claims.connector_id, sets it itself, and no
	// caller can forge it -- the apiserver keys the "demo:" username prefix off
	// the same value.
	//
	// Configuration rather than a constant because it names a deployment's own
	// Dex, and a wrong value is silent in the worst way: every participant is
	// refused with "join through Room Pass" while looking perfectly signed in.
	// The default matches both this cluster and the e2e fixture, so neither has
	// to set it; it exists so renaming the connector is an env var and not a
	// rebuild.
	ParticipantConnectorID string `envconfig:"PARTICIPANT_CONNECTOR_ID" default:"room-pass"`

	// AppOrigin is this application's own origin, used for CSRF origin checks.
	// Never derived from X-Forwarded-* headers.
	AppOrigin string `envconfig:"APP_ORIGIN"`

	// Application cookie keys, supplied by the deployment from a pre-created
	// Secret. Base64 of exactly 32 random bytes each, independent of the Room
	// Pass keys and of the Dex client secret. Replacing them signs everyone
	// out; that is an intentional operation, not seamless rotation.
	AppCookieHashKey  string `envconfig:"APP_COOKIE_HASH_KEY"`
	AppCookieBlockKey string `envconfig:"APP_COOKIE_BLOCK_KEY"`

	// KubernetesAPIServer fixes the destination for participant-token calls.
	// Empty means the in-cluster address.
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
