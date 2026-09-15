package main

// "What may I do?" -- answered by the API server, about the caller, with the
// caller's own token.
//
// This is the read-only half of the authorization story. /auth/whoami already
// answers "who am I" with a SelfSubjectReview; this answers the question the
// room asks next, and it answers it the same way: the participant's own
// credential goes to the apiserver and what comes back is rendered verbatim.
// There is no permission model in this process to disagree with.
//
// Every authenticated identity may ask -- system:basic-user grants `create` on
// selfsubjectrulesreviews to system:authenticated -- so this works for a
// participant whose only other grant is reading one CoffeeConfig. No RBAC
// change is needed to make the table appear.
//
// The review is namespaced on purpose. SelfSubjectRulesReview takes a namespace
// and reports what the caller may do IN it; cluster-scoped grants are not part
// of the answer. That matches what the demo talks about, and the UI says which
// namespace it asked about rather than implying the answer is global.

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/yaml"
)

// selfSubjectRulesReview asks the apiserver to enumerate everything this token
// may do in one namespace.
//
// TypeMeta is restored on the way out for the same reason selfSubjectReview
// does it: client-go clears it on decode, and the YAML view is supposed to be
// the object the apiserver answered with, not a reconstruction.
// Clients are passed in rather than built here so this goes through
// handlerDeps.participantClientsFor -- the one place credentials are turned
// into a client, and the one place a test can substitute a fake apiserver.
func selfSubjectRulesReview(ctx context.Context, clients participantClients, namespace string) (*authorizationv1.SelfSubjectRulesReview, error) {
	review, err := clients.typed.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx,
		&authorizationv1.SelfSubjectRulesReview{
			Spec: authorizationv1.SelfSubjectRulesReviewSpec{Namespace: namespace},
		}, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	review.TypeMeta = metav1.TypeMeta{
		APIVersion: authorizationv1.SchemeGroupVersion.String(),
		Kind:       "SelfSubjectRulesReview",
	}
	return review, nil
}

// authzRule is one row of the table the browser renders.
//
// The apiserver returns rules in whatever order its authorizers produced them,
// and a group/resource pair can appear more than once when several bindings
// contribute. The browser should not have to merge those, so this does it:
// resource rules are flattened to one row per (apiGroup, resource) with the
// union of verbs, which is exactly what `kubectl auth can-i --list` shows and
// what a reader expects to see.
type authzRule struct {
	APIGroup string   `json:"apiGroup"`
	Resource string   `json:"resource"`
	Verbs    []string `json:"verbs"`
	// Names is non-empty when the grant is restricted to specific objects.
	// Leaving it out would render a row that claims more than the caller has.
	Names []string `json:"names,omitempty"`
}

// flattenResourceRules merges the review's rules into one row per
// apiGroup/resource pair.
//
// Rules carrying resourceNames are kept SEPARATE from unrestricted ones for the
// same resource: merging them would show a verb as unconditional when it only
// applies to one named object. A wildcard apiGroup or resource is passed
// through as "*" rather than expanded -- the caller really does hold it on
// everything, and inventing a list of concrete resources here would be this
// process guessing at the cluster's types.
func flattenResourceRules(rules []authorizationv1.ResourceRule) []authzRule {
	type key struct {
		group, resource, names string
	}
	verbs := map[key]map[string]bool{}
	order := []key{}

	for _, rule := range rules {
		names := append([]string(nil), rule.ResourceNames...)
		sort.Strings(names)
		groups := rule.APIGroups
		if len(groups) == 0 {
			groups = []string{""}
		}
		for _, group := range groups {
			for _, resource := range rule.Resources {
				k := key{group: group, resource: resource, names: strings.Join(names, ",")}
				if verbs[k] == nil {
					verbs[k] = map[string]bool{}
					order = append(order, k)
				}
				for _, verb := range rule.Verbs {
					verbs[k][verb] = true
				}
			}
		}
	}

	out := make([]authzRule, 0, len(order))
	for _, k := range order {
		list := make([]string, 0, len(verbs[k]))
		for verb := range verbs[k] {
			list = append(list, verb)
		}
		sort.Strings(list)
		row := authzRule{APIGroup: k.group, Resource: k.resource, Verbs: list}
		if k.names != "" {
			row.Names = strings.Split(k.names, ",")
		}
		out = append(out, row)
	}
	// Stable order so the table does not reshuffle under a poll. Core group
	// ("") first, then alphabetical, which puts the demo's own types together.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].APIGroup != out[j].APIGroup {
			return out[i].APIGroup < out[j].APIGroup
		}
		if out[i].Resource != out[j].Resource {
			return out[i].Resource < out[j].Resource
		}
		return strings.Join(out[i].Names, ",") < strings.Join(out[j].Names, ",")
	})
	return out
}

func registerParticipantAuthzHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg

	// GET /auth/rules -- what this identity may do in the application's
	// namespace, as the API server reports it.
	//
	// ?as=yaml returns the raw SelfSubjectRulesReview as text, the same way
	// /auth/whoami does, for anyone who wants to read the unrendered answer.
	//
	// The SPA polls this: an operator can widen an audience grant mid-demo, and
	// a page that only asked once would keep claiming a refusal that is no
	// longer true. Polling rather than watching is deliberate -- a participant
	// has no permission to watch RBAC, and widening the stream's scope
	// allowlist to let them would undo the point of having one.
	mux.HandleFunc("/auth/rules", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		review, err := selfSubjectRulesReview(ctx, clients, deps.defaultNS)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}

		if r.URL.Query().Get("as") == "yaml" {
			out, err := yaml.Marshal(review)
			if err != nil {
				http.Error(w, "could not render the review", http.StatusInternalServerError)
				return
			}
			// text/plain so a browser shows it instead of downloading it, and
			// nosniff so it is not re-interpreted as anything else.
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			_, _ = w.Write(out)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"namespace": deps.defaultNS,
			"rules":     flattenResourceRules(review.Status.ResourceRules),
			// Incomplete means an authorizer could not enumerate -- a webhook,
			// typically. The page must say so rather than present a short list
			// as the whole truth.
			"incomplete":      review.Status.Incomplete,
			"evaluationError": review.Status.EvaluationError,
		})
	}))
}
