package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// --- flattening -------------------------------------------------------------

// The apiserver may report the same resource from several bindings, and the
// table must not show one resource twice with half its verbs in each row.
func TestFlattenResourceRulesUnionsVerbsForOneResource(t *testing.T) {
	got := flattenResourceRules([]authorizationv1.ResourceRule{
		{APIGroups: []string{"examples.configbutler.ai"}, Resources: []string{"coffeeconfigs"}, Verbs: []string{"get", "list"}},
		{APIGroups: []string{"examples.configbutler.ai"}, Resources: []string{"coffeeconfigs"}, Verbs: []string{"patch", "get"}},
	})

	want := []authzRule{{
		APIGroup: "examples.configbutler.ai",
		Resource: "coffeeconfigs",
		Verbs:    []string{"get", "list", "patch"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// A grant restricted to named objects must never be merged into the
// unrestricted row: doing so would show "patch" as if it applied to every
// object of that type, which is exactly the claim the demo must not make.
func TestFlattenResourceRulesKeepsNamedGrantsSeparate(t *testing.T) {
	got := flattenResourceRules([]authorizationv1.ResourceRule{
		{APIGroups: []string{"examples.configbutler.ai"}, Resources: []string{"quizsessions"}, Verbs: []string{"get", "list"}},
		{
			APIGroups:     []string{"examples.configbutler.ai"},
			Resources:     []string{"quizsessions"},
			ResourceNames: []string{"demo2-round"},
			Verbs:         []string{"patch"},
		},
	})

	if len(got) != 2 {
		t.Fatalf("expected the named grant to stay its own row, got %+v", got)
	}
	var named, open *authzRule
	for i := range got {
		if len(got[i].Names) > 0 {
			named = &got[i]
		} else {
			open = &got[i]
		}
	}
	if named == nil || open == nil {
		t.Fatalf("expected one named and one unrestricted row, got %+v", got)
	}
	if !reflect.DeepEqual(named.Verbs, []string{"patch"}) {
		t.Errorf("named row verbs = %v, want [patch]", named.Verbs)
	}
	if !reflect.DeepEqual(named.Names, []string{"demo2-round"}) {
		t.Errorf("named row names = %v, want [demo2-round]", named.Names)
	}
	if !reflect.DeepEqual(open.Verbs, []string{"get", "list"}) {
		t.Errorf("unrestricted row verbs = %v, want [get list]", open.Verbs)
	}
}

// A core-group rule arrives with an empty APIGroups slice, not with "". Losing
// the row entirely is the failure this guards.
func TestFlattenResourceRulesKeepsCoreGroupRules(t *testing.T) {
	got := flattenResourceRules([]authorizationv1.ResourceRule{
		{Resources: []string{"configmaps"}, Verbs: []string{"get"}},
	})
	if len(got) != 1 || got[0].APIGroup != "" || got[0].Resource != "configmaps" {
		t.Fatalf("core group rule lost: %+v", got)
	}
}

// The table is polled, so an unstable order would make it flicker under a
// grant that did not change.
func TestFlattenResourceRulesIsStablyOrdered(t *testing.T) {
	rules := []authorizationv1.ResourceRule{
		{APIGroups: []string{"examples.configbutler.ai"}, Resources: []string{"quizsubmissions"}, Verbs: []string{"create"}},
		{APIGroups: []string{"configbutler.ai"}, Resources: []string{"commitrequests"}, Verbs: []string{"create"}},
		{Resources: []string{"pods"}, Verbs: []string{"get"}},
	}
	first := flattenResourceRules(rules)
	second := flattenResourceRules(rules)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("ordering is not deterministic:\n%+v\n%+v", first, second)
	}
	if first[0].APIGroup != "" {
		t.Errorf("core group should sort first, got %q", first[0].APIGroup)
	}
}

// --- the endpoints ----------------------------------------------------------

const authzNamespace = "voter"

func roomObject(name, group string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "roompass.configbutler.ai/v1alpha1",
		"kind":       "Room",
		"metadata":   map[string]any{"name": name, "namespace": authzNamespace},
		"spec":       map[string]any{"audienceGroup": group},
	}}
}

// authzFixture wires the two new handler sets against a fake apiserver that
// answers rules reviews with a canned review and tracks RoleBindings for real.
func authzFixture(t *testing.T, review *authorizationv1.SelfSubjectRulesReview, objs ...runtime.Object) (*http.ServeMux, config, *k8sfake.Clientset) {
	t.Helper()
	old := sessionCookieCodec
	sessionCookieCodec = testCodec(t)
	t.Cleanup(func() { sessionCookieCodec = old })

	cfg := testConfig()
	cfg.RoomName = "demo"
	cfg.AudienceCoffeeAdminRole = "voter-audience-coffee-admin"

	// The Role the binding references. Seeded because the handler refuses to
	// create a binding to a Role that does not exist -- a binding like that is
	// a valid object that grants nobody anything, which is the one failure the
	// operator cannot see from the stage.
	typed := k8sfake.NewSimpleClientset(&rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.AudienceCoffeeAdminRole, Namespace: authzNamespace},
	})
	if review != nil {
		// The fake clientset returns a zero-valued review otherwise; a
		// SelfSubjectRulesReview's answer lives entirely in its status, which
		// only the real apiserver fills in.
		typed.PrependReactor("create", "selfsubjectrulesreviews",
			func(k8stesting.Action) (bool, runtime.Object, error) { return true, review, nil })
	}

	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{roomsGVR(): "RoomList"}, objs...)

	deps := handlerDeps{
		cfg:       cfg,
		defaultNS: authzNamespace,
		newClients: func(config, string) (participantClients, error) {
			return participantClients{typed: typed, dynamic: dyn}, nil
		},
	}
	mux := http.NewServeMux()
	registerParticipantAuthzHandlers(mux, deps)
	registerAudienceGrantHandlers(mux, deps)
	return mux, cfg, typed
}

func TestAuthRulesReportsTheFlattenedTableAndNamespace(t *testing.T) {
	mux, cfg, _ := authzFixture(t, &authorizationv1.SelfSubjectRulesReview{
		Status: authorizationv1.SubjectRulesReviewStatus{
			ResourceRules: []authorizationv1.ResourceRule{
				{APIGroups: []string{"examples.configbutler.ai"}, Resources: []string{"coffeeconfigs"}, Verbs: []string{"get", "list", "watch"}},
			},
		},
	})

	body := doJSON[struct {
		Namespace  string      `json:"namespace"`
		Rules      []authzRule `json:"rules"`
		Incomplete bool        `json:"incomplete"`
	}](t, mux, signedInRequest(t, cfg, http.MethodGet, "/auth/rules", ""), http.StatusOK)

	if body.Namespace != authzNamespace {
		t.Errorf("namespace = %q, want %q", body.Namespace, authzNamespace)
	}
	if len(body.Rules) != 1 || body.Rules[0].Resource != "coffeeconfigs" {
		t.Fatalf("rules = %+v", body.Rules)
	}
	// The absence of patch is the whole point of the table before the grant.
	for _, verb := range body.Rules[0].Verbs {
		if verb == "patch" {
			t.Error("patch should not appear before the grant is made")
		}
	}
}

// An authorizer that cannot enumerate returns Incomplete, and a page that
// presented a short list as the whole truth would be lying to the room.
func TestAuthRulesPassesIncompleteThrough(t *testing.T) {
	mux, cfg, _ := authzFixture(t, &authorizationv1.SelfSubjectRulesReview{
		Status: authorizationv1.SubjectRulesReviewStatus{Incomplete: true, EvaluationError: "webhook unavailable"},
	})

	body := doJSON[struct {
		Incomplete      bool   `json:"incomplete"`
		EvaluationError string `json:"evaluationError"`
	}](t, mux, signedInRequest(t, cfg, http.MethodGet, "/auth/rules", ""), http.StatusOK)

	if !body.Incomplete || body.EvaluationError == "" {
		t.Fatalf("incomplete answer was flattened away: %+v", body)
	}
}

func TestAuthRulesServesRawYAMLForTheLink(t *testing.T) {
	mux, cfg, _ := authzFixture(t, &authorizationv1.SelfSubjectRulesReview{
		Status: authorizationv1.SubjectRulesReviewStatus{
			ResourceRules: []authorizationv1.ResourceRule{
				{APIGroups: []string{"examples.configbutler.ai"}, Resources: []string{"coffeeconfigs"}, Verbs: []string{"get"}},
			},
		},
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signedInRequest(t, cfg, http.MethodGet, "/auth/rules?as=yaml", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// text/plain so the browser shows it rather than downloading it.
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type = %q, want text/plain", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "SelfSubjectRulesReview") {
		t.Errorf("YAML does not name the kind it answered with:\n%s", body)
	}
}

func TestAuthRulesRefusesAnUnauthenticatedCaller(t *testing.T) {
	mux, _, _ := authzFixture(t, &authorizationv1.SelfSubjectRulesReview{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth/rules", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// --- the grant switch -------------------------------------------------------

func TestAudienceGrantStartsOffAndTurnsOn(t *testing.T) {
	mux, cfg, typed := authzFixture(t, nil, roomObject("demo", "demo:voter-audience"))

	off := doJSON[struct {
		Granted bool `json:"granted"`
	}](t, mux, signedInRequest(t, cfg, http.MethodGet, "/public/audience/coffee-admin", ""), http.StatusOK)
	if off.Granted {
		t.Fatal("the grant must start off; demo 1 depends on the refusal being real")
	}

	on := doJSON[struct {
		Granted bool   `json:"granted"`
		Group   string `json:"group"`
	}](t, mux, signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", `{"granted":true}`), http.StatusOK)
	if !on.Granted {
		t.Fatal("PUT granted=true did not report the grant on")
	}

	// The subject comes from the Room, not from this process's configuration.
	// A binding naming the wrong group is a valid object that grants nobody
	// anything, so this is the assertion that catches a silent no-op.
	binding, err := typed.RbacV1().RoleBindings(authzNamespace).Get(t.Context(), cfg.AudienceCoffeeAdminRole, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("binding was not created: %v", err)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Name != "demo:voter-audience" {
		t.Fatalf("subjects = %+v, want the Room's audienceGroup", binding.Subjects)
	}
	if binding.Subjects[0].Kind != rbacv1.GroupKind {
		t.Errorf("subject kind = %q, want Group", binding.Subjects[0].Kind)
	}
	if binding.RoleRef.Name != cfg.AudienceCoffeeAdminRole || binding.RoleRef.Kind != "Role" {
		t.Errorf("roleRef = %+v", binding.RoleRef)
	}
}

// The switch is pulled on stage. Flipping it twice in either direction must not
// produce an error the room can see.
func TestAudienceGrantIsIdempotentBothWays(t *testing.T) {
	mux, cfg, _ := authzFixture(t, nil, roomObject("demo", "demo:voter-audience"))

	for i := 0; i < 2; i++ {
		doJSON[map[string]any](t, mux, signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", `{"granted":true}`), http.StatusOK)
	}
	for i := 0; i < 2; i++ {
		body := doJSON[struct {
			Granted bool `json:"granted"`
		}](t, mux, signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", `{"granted":false}`), http.StatusOK)
		if body.Granted {
			t.Fatalf("revoke %d reported the grant still on", i)
		}
	}
}

func TestAudienceGrantRevokeRemovesTheBinding(t *testing.T) {
	mux, cfg, typed := authzFixture(t, nil, roomObject("demo", "demo:voter-audience"))

	doJSON[map[string]any](t, mux, signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", `{"granted":true}`), http.StatusOK)
	doJSON[map[string]any](t, mux, signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", `{"granted":false}`), http.StatusOK)

	if _, err := typed.RbacV1().RoleBindings(authzNamespace).Get(t.Context(), cfg.AudienceCoffeeAdminRole, metav1.GetOptions{}); err == nil {
		t.Fatal("binding survived the revoke")
	}
}

func TestAudienceGrantRejectsABodyWithoutTheField(t *testing.T) {
	mux, cfg, _ := authzFixture(t, nil, roomObject("demo", "demo:voter-audience"))
	for _, body := range []string{`{}`, `{"granted":"yes"}`, `{"on":true}`, ``} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", body))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, rec.Code)
		}
	}
}

// Without a Room there is no audienceGroup to bind, and creating a binding with
// an empty subject would look like success while granting nobody anything.
func TestAudienceGrantFailsWhenTheRoomDeclaresNoAudienceGroup(t *testing.T) {
	mux, cfg, typed := authzFixture(t, nil, roomObject("demo", ""))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", `{"granted":true}`))
	if rec.Code == http.StatusOK {
		t.Fatalf("a Room with no audienceGroup must not produce a binding (status %d)", rec.Code)
	}
	if _, err := typed.RbacV1().RoleBindings(authzNamespace).Get(t.Context(), cfg.AudienceCoffeeAdminRole, metav1.GetOptions{}); err == nil {
		t.Fatal("a binding with no subject was created")
	}
}

func TestAudienceGrantRefusesAnUnauthenticatedCaller(t *testing.T) {
	mux, _, _ := authzFixture(t, nil, roomObject("demo", "demo:voter-audience"))
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(method, "/public/audience/coffee-admin", strings.NewReader(`{"granted":true}`)))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", method, rec.Code)
		}
	}
}

// A state-changing call without the CSRF header must be refused even with a
// valid session cookie -- the switch is a mutation like any other.
func TestAudienceGrantRequiresCSRF(t *testing.T) {
	mux, cfg, _ := authzFixture(t, nil, roomObject("demo", "demo:voter-audience"))
	req := signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", `{"granted":true}`)
	req.Header.Del("X-CSRF-Token")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// A binding to a missing Role is accepted by Kubernetes, looks healthy, and
// grants nothing. The switch must refuse it rather than report success.
func TestAudienceGrantRefusesToBindAMissingRole(t *testing.T) {
	mux, cfg, typed := authzFixture(t, nil, roomObject("demo", "demo:voter-audience"))
	if err := typed.RbacV1().Roles(authzNamespace).Delete(t.Context(), cfg.AudienceCoffeeAdminRole, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("could not remove the Role for this case: %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, signedInRequest(t, cfg, http.MethodPut, "/public/audience/coffee-admin", `{"granted":true}`))
	if rec.Code == http.StatusOK {
		t.Fatalf("binding to a missing Role reported success (status %d)", rec.Code)
	}
	if _, err := typed.RbacV1().RoleBindings(authzNamespace).Get(t.Context(), cfg.AudienceCoffeeAdminRole, metav1.GetOptions{}); err == nil {
		t.Fatal("a binding to a nonexistent Role was created")
	}
}
