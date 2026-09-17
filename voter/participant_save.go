package main

// What a krm-stream editor sends when it saves, and the checks every editable
// resource in this application runs on it.
//
// There is one editing library in the browser, so there is one body shape on
// the wire: the object the editor was holding, identified by uid and
// resourceVersion, plus the merge patch it computed. Decoding that in each
// handler meant the same six refusals written out twice, which is two places
// for "only spec is editable" to stop being true.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// maxPatchBytes bounds the merge patch a participant may send. Without this the
// endpoint would accept an arbitrarily large body and hand it straight to the
// API server.
const maxPatchBytes = 64 * 1024

type saveIntent struct {
	UID             string         `json:"uid"`
	ResourceVersion string         `json:"resourceVersion"`
	Patch           map[string]any `json:"patch"`
}

// decodeSaveIntent reads the body of a save, or writes the refusal and reports
// false. Callers are one `if !ok { return }`, which is the point: a handler
// that forgets a check cannot exist, because there is nothing left to forget.
//
// `spec` is the only top-level key accepted. The editability policy in the
// browser says the same thing, but the browser is not what enforces it.
func decodeSaveIntent(w http.ResponseWriter, r *http.Request) (saveIntent, bool) {
	var intent saveIntent

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPatchBytes+1))
	if err != nil {
		http.Error(w, "failed to read patch body", http.StatusBadRequest)
		return intent, false
	}
	if len(body) > maxPatchBytes {
		http.Error(w, "patch body too large", http.StatusRequestEntityTooLarge)
		return intent, false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		http.Error(w, "empty patch body", http.StatusBadRequest)
		return intent, false
	}
	if !json.Valid(body) {
		http.Error(w, "patch body is not valid JSON", http.StatusBadRequest)
		return intent, false
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil || intent.UID == "" || intent.ResourceVersion == "" || intent.Patch == nil {
		http.Error(w, "uid, resourceVersion and patch are required", http.StatusBadRequest)
		return intent, false
	}
	for key := range intent.Patch {
		if key != "spec" {
			http.Error(w, "only spec is editable", http.StatusBadRequest)
			return intent, false
		}
	}
	return intent, true
}

// changeReason is the note the editor typed into "why are you making this
// change?", carried out of band because it describes the save rather than the
// object. Trimmed and bounded here so neither a Git commit message nor an
// annotation can be handed an unbounded string.
func changeReason(r *http.Request) string {
	reason := strings.TrimSpace(r.Header.Get("X-Change-Reason"))
	if len(reason) > maxChangeReasonBytes {
		reason = strings.TrimSpace(reason[:maxChangeReasonBytes])
	}
	return reason
}

const maxChangeReasonBytes = 1024
