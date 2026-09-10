package main

import (
	"github.com/kelseyhightower/envconfig"
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

	// ConfigButlerGitTargetName names the ConfigButler GitTarget whose open
	// commit window should be finalized after a successful CoffeeConfig patch.
	// Set to "" to disable the save-message side effect entirely.
	ConfigButlerGitTargetName string `envconfig:"CONFIGBUTLER_GIT_TARGET_NAME" default:"voter-demo"`
	// ConfigButlerCommitRequestNamespace overrides where CommitRequest objects
	// are created. Empty means "use the runtime namespace", which must also be
	// the GitTarget's namespace.
	ConfigButlerCommitRequestNamespace string `envconfig:"CONFIGBUTLER_COMMITREQUEST_NAMESPACE"`

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
