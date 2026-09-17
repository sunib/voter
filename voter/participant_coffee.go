package main

// The coffee endpoints in OIDC mode.
//
// Everything here talks to Kubernetes with the PARTICIPANT's ID token. That is
// the whole point: the audit event names the person who clicked, so
// gitops-reverser can author a Git commit in their name. No impersonation, and
// no fallback to the ServiceAccount if the participant is denied -- a 403 is
// the honest answer and it stays a 403.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ConfigButler/krm-stream/gateway"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func registerParticipantCoffeeHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg
	ns := deps.defaultNS
	name := cfg.CoffeeConfigName

	mux.HandleFunc("/public/coffeeconfig", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			http.Error(w, "could not build a client for your session", http.StatusInternalServerError)
			return
		}

		switch r.Method {
		case http.MethodGet:
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			obj, err := clients.dynamic.Resource(coffeeConfigGVR()).Namespace(ns).
				Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			// Keep the same resource shape as the stream. Business DTOs omit
			// unknown fields and zero values and cannot serve as an edit base.
			projected, _ := gateway.Project(gateway.ProjectionFull, obj.Object)
			writeJSON(w, http.StatusOK, projected)

		case http.MethodPatch:
			// Body shape, size and "only spec" all live in participant_save.go, so
			// this handler and the Database editor cannot disagree about them.
			intent, ok := decodeSaveIntent(w, r)
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			resource := clients.dynamic.Resource(coffeeConfigGVR()).Namespace(ns)
			current, err := resource.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			if string(current.GetUID()) != intent.UID {
				http.Error(w, "configuration was replaced; open it as a new editor", http.StatusConflict)
				return
			}
			intent.Patch["metadata"] = map[string]any{"uid": intent.UID, "resourceVersion": intent.ResourceVersion}
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

			// The CoffeeConfig write and the CommitRequest are two separate
			// Kubernetes operations. If the second fails, the first still
			// happened -- so report PARTIAL success rather than pretending the
			// pair was atomic or that nothing occurred.
			response := map[string]any{"saved": true}

			if target := strings.TrimSpace(cfg.ConfigButlerGitTargetName); target != "" {
				crNS := strings.TrimSpace(cfg.ConfigButlerCommitRequestNamespace)
				if crNS == "" {
					crNS = ns
				}
				crName, crErr := createParticipantCommitRequest(ctx, clients, crNS, target,
					changeReason(r), cfg.ConfigButlerCloseDelaySeconds)
				switch {
				case crErr != nil:
					log.Printf("commitrequest: create failed sub=%s target=%s: %v", s.Subject, target, crErr)
					response["commitRequested"] = false
					response["commitError"] = "Your change was saved to Kubernetes, but asking ConfigButler to commit it failed."
				default:
					log.Printf("commitrequest: created name=%s sub=%s target=%s", crName, s.Subject, target)
					response["commitRequested"] = true
					response["commitRequest"] = crName
				}
			}

			writeJSON(w, http.StatusOK, response)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
}

func createParticipantCommitRequest(ctx context.Context, clients participantClients, ns, target, message string, closeDelaySeconds int32) (string, error) {
	return createNamedCommitRequest(ctx, clients, ns, target, "coffee-save-", message, closeDelaySeconds)
}

// createNamedCommitRequest is the same request with the generateName as a
// parameter, so a Database save is recognisable from a coffee one in
// `kubectl get commitrequests` without reading the message.
func createNamedCommitRequest(ctx context.Context, clients participantClients, ns, target, generateName, message string, closeDelaySeconds int32) (string, error) {
	spec := map[string]any{"gitTargetRef": map[string]any{"name": target}}
	if message != "" {
		spec["message"] = message
	}
	// Without this the request races the write it exists to publish, and loses:
	// nothing is attached, so the message above is dropped and the edit waits out
	// the target's own window instead. See ConfigButlerCloseDelaySeconds.
	// int64, not int32: unstructured values are deep-copied as JSON, and any
	// narrower integer panics there rather than at compile time.
	if closeDelaySeconds > 0 {
		spec["closeDelaySeconds"] = int64(closeDelaySeconds)
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": commitRequestAPIVersion,
		"kind":       "CommitRequest",
		"metadata": map[string]any{
			"generateName": generateName,
			"namespace":    ns,
		},
		"spec": spec,
	}}
	created, err := clients.dynamic.Resource(commitRequestGVR()).Namespace(ns).
		Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	return created.GetName(), nil
}

// writeParticipantKubeError preserves the Kubernetes verdict. A 403 stays a
// 403: the participant genuinely may not do that, and dressing it up as a
// server error would hide the RBAC decision the demo is trying to show.
func writeParticipantKubeError(w http.ResponseWriter, err error) {
	var statusErr interface{ Status() metav1.Status }
	if errors.As(err, &statusErr) {
		st := statusErr.Status()
		code := int(st.Code)
		if code == 0 {
			code = http.StatusInternalServerError
		}
		// 401 here means the token was rejected -- expired, most likely.
		if code == http.StatusUnauthorized {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":    "your session has expired, please sign in again",
				"loginUrl": "/auth/login",
			})
			return
		}
		writeJSON(w, code, map[string]any{"error": st.Message, "reason": string(st.Reason)})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"error": "the Kubernetes API could not be reached"})
}
