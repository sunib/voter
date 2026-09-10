package main

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

type config struct {
	Host                           string        `envconfig:"HOST" default:"0.0.0.0"`
	Port                           string        `envconfig:"PORT" default:"8080"`
	CookieSecure                   bool          `envconfig:"COOKIE_SECURE" default:"false"`
	SessionCookieName              string        `envconfig:"SESSION_COOKIE_NAME" default:"demo_session"`
	SessionCookieMaxAgeSecs        int           `envconfig:"SESSION_COOKIE_MAX_AGE_SECONDS" default:"7200"`
	ParticipantCookieName          string        `envconfig:"PARTICIPANT_COOKIE_NAME" default:"participant_session"`
	ParticipantSessionMaxAgeSecs   int           `envconfig:"PARTICIPANT_SESSION_MAX_AGE_SECONDS" default:"43200"`
	AdminCookieName                string        `envconfig:"ADMIN_COOKIE_NAME" default:"coffee_admin_session"`
	AdminSessionMaxAgeSecs         int           `envconfig:"ADMIN_SESSION_MAX_AGE_SECONDS" default:"28800"`
	AdminPassword                  string        `envconfig:"ADMIN_PASSWORD" default:"testnetcoffee"`
	DemoAccessCode                 string        `envconfig:"DEMO_ACCESS_CODE"`
	JoinCodeRotate                 time.Duration `envconfig:"JOIN_CODE_ROTATE_SECONDS" default:"15s"`
	JoinCodeTTL                    time.Duration `envconfig:"JOIN_CODE_TTL_SECONDS" default:"7200s"`
	JoinCodeLength                 int           `envconfig:"JOIN_CODE_LENGTH" default:"4"`
	ForwardServiceAccount          string        `envconfig:"FORWARD_SA"`
	ForwardServiceAccountNamespace string        `envconfig:"FORWARD_SA_NAMESPACE"`
	CoffeeConfigName               string        `envconfig:"COFFEE_CONFIG_NAME" default:"testnet-coffee"`

	// ConfigButlerGitTargetName names the ConfigButler GitTarget whose open
	// commit window should be finalized after a successful CoffeeConfig patch.
	// Set to "" to disable the save-message side effect entirely.
	ConfigButlerGitTargetName string `envconfig:"CONFIGBUTLER_GIT_TARGET_NAME" default:"voter-demo"`
	// ConfigButlerCommitRequestNamespace overrides where CommitRequest objects
	// are created. Empty means "use the runtime namespace" (the same namespace
	// the auth-service runs in, which must also be the GitTarget's namespace).
	ConfigButlerCommitRequestNamespace string `envconfig:"CONFIGBUTLER_COMMITREQUEST_NAMESPACE"`
	// ConfigButlerIdentityExtrasEnabled controls whether impersonated writes
	// carry the configbutler.ai/claims/{display-name,email} extras. Requires
	// matching RBAC on authentication.k8s.io/userextras/...; flip off if that
	// RBAC is not yet applied to keep the demo working.
	ConfigButlerIdentityExtrasEnabled bool `envconfig:"CONFIGBUTLER_IDENTITY_EXTRAS_ENABLED" default:"true"`

	// --- OIDC mode ----------------------------------------------------------
	//
	// OIDCEnabled selects between two MUTUALLY EXCLUSIVE authentication models:
	//
	//   false — legacy: browser-asserted identity + ServiceAccount
	//           impersonation. Kept only so the old demo still runs during
	//           migration. Must not be exposed publicly.
	//   true  — Dex/Room Pass: the backend is a confidential OIDC client and
	//           participant Kubernetes calls carry the participant's own ID
	//           token.
	//
	// There is deliberately no fallback between them. In OIDC mode a failed
	// participant request is an error, never a retry as the ServiceAccount.
	OIDCEnabled      bool   `envconfig:"OIDC_ENABLED" default:"false"`
	OIDCIssuerURL    string `envconfig:"OIDC_ISSUER_URL"`
	OIDCClientID     string `envconfig:"OIDC_CLIENT_ID"`
	OIDCClientSecret string `envconfig:"OIDC_CLIENT_SECRET"`
	// OIDCRedirectURL must match a redirect URI registered on the Dex client
	// exactly. It is configuration, never derived from a request header.
	OIDCRedirectURL string `envconfig:"OIDC_REDIRECT_URL"`

	// AppOrigin is this application's own origin, used for CSRF origin checks.
	// Also never derived from X-Forwarded-* headers.
	AppOrigin string `envconfig:"APP_ORIGIN"`

	// Application cookie keys, supplied by the deployment from a pre-created
	// Secret. Base64 of exactly 32 random bytes each, independent of the Room
	// Pass keys and of the Dex client secret. Replacing them signs everyone
	// out; that is an intentional operation, not seamless rotation.
	AppCookieHashKey  string `envconfig:"APP_COOKIE_HASH_KEY"`
	AppCookieBlockKey string `envconfig:"APP_COOKIE_BLOCK_KEY"`

	// StaticDir is the built frontend bundle served by this process. The
	// container image sets it to /srv/www; leaving it empty (the local Vite
	// setup) serves a plain-text API description at / instead.
	StaticDir string `envconfig:"STATIC_DIR"`

	// KubernetesAPIServer fixes the destination for participant-token calls.
	// Empty means the in-cluster address.
	KubernetesAPIServer string `envconfig:"KUBERNETES_API_SERVER"`
}

func loadConfig() (config, error) {
	var cfg config
	if err := envconfig.Process("", &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}
