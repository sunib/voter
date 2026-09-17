package main

// The Database endpoints: what the teams in the room are asking the platform
// team for.
//
// Same rules as coffee (participant_coffee.go): every call is made with the
// PARTICIPANT's own ID token, a refusal is Kubernetes' refusal and stays one,
// and nothing here falls back to the ServiceAccount. The difference is what the
// collection is FOR. A CoffeeConfig is one shared object everybody amends; a
// Database is one object per request, created by the team that wants it, so
// this file has a create and a list where coffee has neither.
//
// There is deliberately no delete. Removing somebody else's request is not a
// thing this page does, and the Role behind it does not grant the verb.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ConfigButler/krm-stream/gateway"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// intentAnnotation carries the note the requester typed into "why are you
// making this change?".
//
// For the coffee menu that note becomes a Git commit message and nothing else,
// because the CoffeeConfig itself is in Git and the commit is the record. A
// Database has no GitTarget watching it yet, so the note would simply be
// dropped -- and the note is half of what the list page exists to show. It
// belongs on the object either way: when a target does start watching these,
// the annotation travels into Git with the spec it explains.
const intentAnnotation = "platform.configbutler.ai/intent"

// A Kubernetes object name, checked here so a bad one is a readable 400 rather
// than an apiserver 422 about a regular expression.
var databaseNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func registerParticipantDatabaseHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg
	ns := deps.defaultNS

	mux.HandleFunc("/public/databases", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			http.Error(w, "could not build a client for your session", http.StatusInternalServerError)
			return
		}
		resource := clients.dynamic.Resource(databaseGVR()).Namespace(ns)

		switch r.Method {
		case http.MethodGet:
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			list, err := resource.List(ctx, metav1.ListOptions{})
			if err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			// Projected one by one, for the same reason the coffee GET is:
			// the list is the first paint of a screen the stream then takes
			// over, and the two must not disagree about the shape of an object.
			items := make([]any, 0, len(list.Items))
			for i := range list.Items {
				projected, _ := gateway.Project(gateway.ProjectionFull, list.Items[i].Object)
				items = append(items, projected)
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})

		case http.MethodPost:
			var body struct {
				Name string         `json:"name"`
				Spec map[string]any `json:"spec"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPatchBytes))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and spec are required"})
				return
			}
			name := strings.TrimSpace(body.Name)
			if !databaseNamePattern.MatchString(name) {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "The name must be lowercase letters, digits and dashes, and start and end with a letter or digit.",
				})
				return
			}
			if body.Spec == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and spec are required"})
				return
			}
			// The CRD's own required fields, enum values and patterns are the
			// validation. Restating them here would give the room two answers to
			// the same question, and the apiserver's is the one that counts --
			// so its message is what the screen shows.
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": databaseAPIVersion,
				"kind":       "Database",
				"metadata": map[string]any{
					"name":      name,
					"namespace": ns,
				},
				"spec": body.Spec,
			}}
			if reason := changeReason(r); reason != "" {
				obj.SetAnnotations(map[string]string{intentAnnotation: reason})
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			created, err := resource.Create(ctx, obj, metav1.CreateOptions{})
			if err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			response := map[string]any{"saved": true, "name": created.GetName()}
			addCommitReceipt(ctx, response, deps, clients, s, "database-create-", changeReason(r))
			writeJSON(w, http.StatusCreated, response)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))

	mux.HandleFunc("/public/databases/{name}", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			http.Error(w, "could not build a client for your session", http.StatusInternalServerError)
			return
		}
		name := r.PathValue("name")
		resource := clients.dynamic.Resource(databaseGVR()).Namespace(ns)

		switch r.Method {
		case http.MethodGet:
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			obj, err := resource.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			projected, _ := gateway.Project(gateway.ProjectionFull, obj.Object)
			writeJSON(w, http.StatusOK, projected)

		case http.MethodPatch:
			intent, ok := decodeSaveIntent(w, r)
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			current, err := resource.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			if string(current.GetUID()) != intent.UID {
				http.Error(w, "this database was replaced; open it as a new editor", http.StatusConflict)
				return
			}
			metadata := map[string]any{"uid": intent.UID, "resourceVersion": intent.ResourceVersion}
			// The note goes in the same merge patch as the spec it explains, so
			// one write carries both and there is no window in which the object
			// records a change nobody gave a reason for.
			if reason := changeReason(r); reason != "" {
				metadata["annotations"] = map[string]any{intentAnnotation: reason}
			}
			intent.Patch["metadata"] = metadata
			patch, err := json.Marshal(intent.Patch)
			if err != nil {
				http.Error(w, "invalid patch", http.StatusBadRequest)
				return
			}
			if err = gateway.ValidateMergePatch(gateway.ProjectionFull, current.Object, patch); err != nil {
				http.Error(w, "patch touches a protected field", http.StatusBadRequest)
				return
			}
			if _, err = resource.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			response := map[string]any{"saved": true}
			addCommitReceipt(ctx, response, deps, clients, s, "database-save-", changeReason(r))
			writeJSON(w, http.StatusOK, response)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
}

// addCommitReceipt finalizes a ConfigButler commit window for the Database
// GitTarget, if there is one.
//
// There is not one yet, and that is the default: DatabaseGitTargetName is
// empty, so this returns without doing anything and the receipt simply carries
// no commit fields. Pointing it at the coffee target instead would be worse
// than doing nothing -- it would close the window that demo 1's menu edit is
// waiting on, from a page that has nothing to do with it.
//
// When a target does start watching Databases, setting the env var is the whole
// change: the shape of the receipt, and what the screen does with it, already
// match what the coffee editor sends.
func addCommitReceipt(ctx context.Context, response map[string]any, deps handlerDeps, clients participantClients, s participantSession, generateName, reason string) {
	target := strings.TrimSpace(deps.cfg.ConfigButlerDatabaseGitTargetName)
	if target == "" {
		return
	}
	crNS := strings.TrimSpace(deps.cfg.ConfigButlerCommitRequestNamespace)
	if crNS == "" {
		crNS = deps.defaultNS
	}
	// The write above already happened. If this fails, say so as PARTIAL
	// success: the two are separate Kubernetes operations and pretending they
	// were atomic would be a lie in whichever direction we told it.
	crName, err := createNamedCommitRequest(ctx, clients, crNS, target, generateName, reason, deps.cfg.ConfigButlerCloseDelaySeconds)
	if err != nil {
		log.Printf("commitrequest: create failed sub=%s target=%s: %v", s.Subject, target, err)
		response["commitRequested"] = false
		response["commitError"] = "Your change was saved to Kubernetes, but asking ConfigButler to commit it failed."
		return
	}
	log.Printf("commitrequest: created name=%s sub=%s target=%s", crName, s.Subject, target)
	response["commitRequested"] = true
	response["commitRequest"] = crName
}
