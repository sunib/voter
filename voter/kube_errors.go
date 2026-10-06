package main

import (
	"errors"
	"log"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// writeKubeError keeps the Kubernetes verdict -- a 404 stays a 404 -- rather
// than every failure reading as a server error.
//
// Voter calls Kubernetes only as itself now, so a 401 or 403 is about Voter's
// own credential or Role, never about the person asking: it is logged as a
// deployment fault and reported as a 502, not as "please sign in".
func writeKubeError(w http.ResponseWriter, err error) {
	var statusErr interface{ Status() metav1.Status }
	if errors.As(err, &statusErr) {
		st := statusErr.Status()
		code := int(st.Code)
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			log.Printf("kubernetes: Voter's own credential was refused (%d): %s", code, st.Message)
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "the application could not read its configuration from Kubernetes"})
			return
		}
		if code == 0 {
			code = http.StatusInternalServerError
		}
		writeJSON(w, code, map[string]any{"error": st.Message, "reason": string(st.Reason)})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"error": "the Kubernetes API could not be reached"})
}
