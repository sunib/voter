package main

// The switch the operator pulls to widen what the room may do.
//
// Demo 1 shows the audience being refused the coffee admin page. Demo 2 hands
// it to them, live, in front of everyone -- and the interesting part is that
// nothing here is an application permission. The grant is a RoleBinding, the
// operator's own token creates it, and every phone in the room discovers the
// new permission the same way it discovered the old refusal: by asking the API
// server what it may do (participant_authz.go).
//
// Why a RoleBinding rather than editing the Role's rules:
//
//   - The Role is static, lives in Git, and is reviewable. What changes at
//     runtime is only whether it is BOUND, which is one object existing or not
//     -- an atomic toggle with no partial state to reason about.
//   - Creating and deleting is idempotent in a way that patching a rules array
//     is not. Two operators racing the switch cannot produce a half-applied
//     grant.
//
// Why the binding is deliberately NOT in the Flux kustomization: Flux would
// recreate whatever this deletes, within the reconcile interval. The Role is
// reconciled forever; the binding is created here and mirrored to the audit
// trail by gitops-reverser, which is a record rather than a source. That split
// is the same one the runbook draws for the CoffeeConfig -- see
// docs/demo-runbook.md, "Two repositories".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func roomsGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "roompass.configbutler.ai",
		Version:  "v1alpha1",
		Resource: "rooms",
	}
}

// audienceGroupFromRoom reads the group every participant lands in from the
// Room object, rather than taking it from configuration.
//
// The Room already declares it (spec.audienceGroup), Room Pass puts every
// enrolled participant in it, and the apiserver's claim validation requires the
// demo: prefix on it. A second copy in this process's environment would be a
// second place for it to drift, and a RoleBinding naming the wrong group fails
// silently -- it is a perfectly valid object that grants nobody anything.
//
// Read with the caller's own token. An identity that cannot read the Room
// cannot create this binding either, so the refusal arrives here rather than
// two steps later with a less obvious message.
func audienceGroupFromRoom(ctx context.Context, clients participantClients, namespace, roomName string) (string, error) {
	room, err := clients.dynamic.Resource(roomsGVR()).Namespace(namespace).Get(ctx, roomName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	group, found, err := unstructuredString(room.Object, "spec", "audienceGroup")
	if err != nil || !found || group == "" {
		return "", fmt.Errorf("room %q declares no spec.audienceGroup", roomName)
	}
	return group, nil
}

func unstructuredString(obj map[string]any, path ...string) (string, bool, error) {
	current := any(obj)
	for _, key := range path {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false, nil
		}
		current, ok = m[key]
		if !ok {
			return "", false, nil
		}
	}
	s, ok := current.(string)
	if !ok {
		return "", false, errors.New("not a string")
	}
	return s, true, nil
}

// audienceCoffeeAdminBinding is the object the switch creates.
//
// The RoleBinding and the Role share one name: there is exactly one of each,
// and two names would be two things to keep in step for no benefit.
func audienceCoffeeAdminBinding(name, namespace, group string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				// Says who creates this and why it is not in the GitOps repo.
				// Anyone finding it with kubectl should be able to tell it is
				// deliberate rather than something that escaped a kustomization.
				"app.kubernetes.io/managed-by": "voter",
				"voter.configbutler.ai/grant":  "coffee-admin",
			},
			Annotations: map[string]string{
				"voter.configbutler.ai/description": "Created live from the operator page. Not a Flux resource; " +
					"gitops-reverser mirrors it to the audit trail.",
			},
		},
		Subjects: []rbacv1.Subject{{
			Kind:     rbacv1.GroupKind,
			APIGroup: rbacv1.GroupName,
			Name:     group,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     name,
		},
	}
}

func registerAudienceGrantHandlers(mux *http.ServeMux, deps handlerDeps) {
	cfg := deps.cfg
	name := cfg.AudienceCoffeeAdminRole

	// GET  /public/audience/coffee-admin -- is the grant on?
	// PUT  /public/audience/coffee-admin -- turn it on or off.
	//
	// Both use the caller's own token, so there is no operator check in this
	// process. A participant reading this gets the API server's 403 for
	// rolebindings, which is the correct answer and a better one than anything
	// this code could invent.
	mux.HandleFunc("/public/audience/coffee-admin", requireParticipant(cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		bindings := clients.typed.RbacV1().RoleBindings(deps.defaultNS)

		switch r.Method {
		case http.MethodGet:
			_, err := bindings.Get(ctx, name, metav1.GetOptions{})
			switch {
			case err == nil:
				writeJSON(w, http.StatusOK, map[string]any{"granted": true, "name": name})
			case apierrors.IsNotFound(err):
				// Not an error: "no such binding" IS the answer, and it is the
				// state the demo starts in.
				writeJSON(w, http.StatusOK, map[string]any{"granted": false, "name": name})
			default:
				writeParticipantKubeError(w, err)
			}

		case http.MethodPut:
			var body struct {
				Granted *bool `json:"granted"`
			}
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&body); err != nil || body.Granted == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "granted must be true or false"})
				return
			}

			if !*body.Granted {
				err := bindings.Delete(ctx, name, metav1.DeleteOptions{})
				// Already gone is the state that was asked for. Reporting 404
				// would make a switch flipped twice look broken.
				if err != nil && !apierrors.IsNotFound(err) {
					writeParticipantKubeError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"granted": false, "name": name})
				return
			}

			// A RoleBinding may reference a Role that does not exist. The API
			// server accepts it, the object looks healthy, the switch reports
			// success -- and nobody is granted anything. That is the worst
			// possible failure on stage, because it is invisible, so refuse it
			// here instead.
			//
			// Forbidden is treated as "cannot verify" rather than as a failure:
			// an identity allowed to bind but not to read Roles is unusual but
			// legitimate, and refusing it would be this code inventing a
			// permission requirement the cluster does not have.
			if _, err := clients.typed.RbacV1().Roles(deps.defaultNS).Get(ctx, name, metav1.GetOptions{}); err != nil {
				if apierrors.IsNotFound(err) {
					writeJSON(w, http.StatusFailedDependency, map[string]string{
						"error": fmt.Sprintf("Role %q does not exist in %s. It is created by GitOps; "+
							"binding to a missing Role would grant nobody anything.", name, deps.defaultNS),
					})
					return
				}
				if !apierrors.IsForbidden(err) {
					writeParticipantKubeError(w, err)
					return
				}
			}

			group, err := audienceGroupFromRoom(ctx, clients, deps.defaultNS, cfg.RoomName)
			if err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			_, err = bindings.Create(ctx, audienceCoffeeAdminBinding(name, deps.defaultNS, group), metav1.CreateOptions{})
			// Already bound is the state that was asked for, as above.
			if err != nil && !apierrors.IsAlreadyExists(err) {
				writeParticipantKubeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"granted": true, "name": name, "group": group})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
}
