package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var quizSessions = schema.GroupVersionResource{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "quizsessions"}
var quizSubmissions = schema.GroupVersionResource{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "quizsubmissions"}

// The two labels every ballot carries. Both are read twice over: by this
// application, which selects results on the round and shows the submitter, and
// by gitops-reverser, which files the mirrored document by them -- one folder
// per round in one target, one file per person in another.
//
// roundLabel holds the round's NAME, not its UID. That is a deliberate trade --
// a round recreated under the same name inherits the old one's ballots -- and it
// is written up, with what keeps it closed and how to recover, as entry 1 of
// docs/deliberate-simplifications.md. Staleness is unaffected: the vote handler's
// resourceVersion check catches a recreated round as surely as an edited one.
const (
	roundLabel     = "voter.configbutler.ai/round"
	submitterLabel = "voter.configbutler.ai/submitter"
)

type quizQuestion struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Title    string   `json:"title"`
	Required bool     `json:"required,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
}
type quizAnswer struct {
	QuestionID   string    `json:"questionId"`
	SingleChoice *string   `json:"singleChoice,omitempty"`
	MultiChoice  *[]string `json:"multiChoice,omitempty"`
	Number       *float64  `json:"number,omitempty"`
	FreeText     *string   `json:"freeText,omitempty"`
}
type quizSpec struct {
	State     string         `json:"state"`
	Questions []quizQuestion `json:"questions"`
}

func decodeQuizSpec(obj *unstructured.Unstructured) (quizSpec, error) {
	var spec quizSpec
	b, err := json.Marshal(obj.Object["spec"])
	if err == nil {
		err = json.Unmarshal(b, &spec)
	}
	return spec, err
}

func validateQuizAnswers(questions []quizQuestion, answers []quizAnswer) error {
	byID := make(map[string]quizQuestion, len(questions))
	for _, q := range questions {
		byID[q.ID] = q
	}
	seen := map[string]bool{}
	for _, a := range answers {
		q, ok := byID[a.QuestionID]
		if !ok || seen[a.QuestionID] {
			return fmt.Errorf("unknown or duplicate question: %s", a.QuestionID)
		}
		seen[a.QuestionID] = true
		fields := 0
		for _, present := range []bool{a.SingleChoice != nil, a.MultiChoice != nil, a.Number != nil, a.FreeText != nil} {
			if present {
				fields++
			}
		}
		valid := fields == 1
		switch q.Type {
		case "singleChoice":
			valid = valid && a.SingleChoice != nil && slices.Contains(q.Choices, *a.SingleChoice)
		case "multiChoice":
			valid = valid && a.MultiChoice != nil
			if a.MultiChoice != nil {
				choices := map[string]bool{}
				for _, c := range *a.MultiChoice {
					if choices[c] || !slices.Contains(q.Choices, c) {
						valid = false
					}
					choices[c] = true
				}
				valid = valid && len(*a.MultiChoice) <= 20 && (!q.Required || len(*a.MultiChoice) > 0)
			}
		case "number", "scale0to10":
			valid = valid && a.Number != nil
			if a.Number != nil {
				valid = valid && (q.Min == nil || *a.Number >= *q.Min) && (q.Max == nil || *a.Number <= *q.Max)
				if q.Type == "scale0to10" {
					valid = valid && *a.Number >= 0 && *a.Number <= 10
				}
			}
		case "freeText":
			valid = valid && a.FreeText != nil && len([]rune(*a.FreeText)) <= 2000 && (!q.Required || strings.TrimSpace(*a.FreeText) != "")
		default:
			valid = false
		}
		if !valid {
			return fmt.Errorf("invalid answer for %s", q.Title)
		}
	}
	for _, q := range questions {
		if q.Required && !seen[q.ID] {
			return fmt.Errorf("answer required: %s", q.Title)
		}
	}
	return nil
}

// One QuizSubmission name per enrolled identity and round: "<round>-<who>".
// Name uniqueness in the API server, not application bookkeeping, is what keeps
// a vote single-use.
//
// No longer a hash, so the room can read the submission list as it fills up.
// That this still keys on IDENTITY rests on a Room Pass property rather than on
// anything here -- enrolling under a name already taken is refused, so one
// display name is one Participant is one Kubernetes subject. Entry 2 of
// docs/deliberate-simplifications.md has the full argument and, more usefully,
// what would break it.
//
// Lowercase because an object name is DNS-1123 where a label value may be
// mixed; that lowering reproduces Room Pass's own participant id exactly, so
// this name and the participant's address agree by construction.
func submissionName(roundName, displayName string) string {
	return roundName + "-" + strings.ToLower(displayName)
}

// The API owns voting rules and resource construction; Kubernetes authorizes
// every operation with the participant token. These rules are not an admission
// policy: direct Kubernetes access can bypass application validation.
func registerParticipantQuizHandlers(mux *http.ServeMux, deps handlerDeps) {
	mux.HandleFunc("/public/rounds", requireParticipant(deps.cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		list, err := clients.dynamic.Resource(quizSessions).Namespace(deps.defaultNS).List(ctx, metav1.ListOptions{})
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		writeJSON(w, 200, list)
	}))
	mux.HandleFunc("/public/rounds/{name}", requireParticipant(deps.cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		round, err := clients.dynamic.Resource(quizSessions).Namespace(deps.defaultNS).Get(ctx, r.PathValue("name"), metav1.GetOptions{})
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		if r.Method == http.MethodGet {
			// Tell a returning voter before they fill in the form. The atomic create below
			// stays authoritative, so a lookup failure costs only the early warning.
			_, err := clients.dynamic.Resource(quizSubmissions).Namespace(deps.defaultNS).Get(ctx, submissionName(round.GetName(), s.DisplayName), metav1.GetOptions{})
			writeJSON(w, 200, map[string]any{"round": round, "voted": err == nil})
			return
		}
		spec, err := decodeQuizSpec(round)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		if spec.State != "live" {
			writeJSON(w, 409, map[string]string{"error": "This round is not open for voting."})
			return
		}
		// Voting is for people who came through the door. requireParticipant
		// checks that a session is valid, not which Dex connector issued it, so
		// an operator on the github connector reaches this handler with a
		// session that never passed Room Pass -- and therefore with a display
		// name nothing folded, which would be refused as a label value and cost
		// them their ballot with a 422 nobody could read.
		//
		// This is an APPLICATION rule, not an RBAC one, and the difference is
		// the demo rather than an oversight: the operator is cluster-admin, so
		// the API server has no objection at all to the same write made with
		// kubectl. Entry 4 of docs/deliberate-simplifications.md.
		if s.Connector != deps.cfg.ParticipantConnectorID {
			writeJSON(w, 403, map[string]string{
				"error": "Voting is for people who joined through Room Pass. Scan the QR code to join the room.",
				"code":  "NotAParticipant",
			})
			return
		}
		var req struct {
			ResourceVersion string       `json:"resourceVersion"`
			Answers         []quizAnswer `json:"answers"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "Invalid vote."})
			return
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			writeJSON(w, 400, map[string]string{"error": "Invalid vote."})
			return
		}
		// resourceVersion alone, where this used to compare the round's UID too.
		// A recreated round gets a fresh resourceVersion just as an edited one
		// does, so this catches both and the UID added nothing.
		if req.ResourceVersion == "" || req.ResourceVersion != round.GetResourceVersion() {
			writeJSON(w, 409, map[string]string{"error": "The round changed. Reload the questions before voting."})
			return
		}
		if err := validateQuizAnswers(spec.Questions, req.Answers); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		// Atomic create keeps retries and concurrent tabs to one vote, whatever the GET
		// reported. Recreating a round starts fresh; reopening it does not.
		name := submissionName(round.GetName(), s.DisplayName)
		answers, _ := json.Marshal(req.Answers)
		var answerObjects []any
		_ = json.Unmarshal(answers, &answerObjects)
		if answerObjects == nil {
			answerObjects = []any{}
		}
		obj := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "QuizSubmission",
			"metadata": map[string]any{"name": name, "namespace": deps.defaultNS, "labels": map[string]any{roundLabel: round.GetName(), submitterLabel: s.DisplayName}},
			"spec":     map[string]any{"sessionRef": map[string]any{"group": quizSessions.Group, "kind": "QuizSession", "name": round.GetName()}, "submittedAt": time.Now().UTC().Format(time.RFC3339), "answers": answerObjects},
		}}
		_, err = clients.dynamic.Resource(quizSubmissions).Namespace(deps.defaultNS).Create(ctx, obj, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			writeJSON(w, 409, map[string]string{"error": "You have already voted in this round.", "code": "AlreadyVoted"})
			return
		}
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		writeJSON(w, 201, map[string]string{"name": name})
	}))
	// Opening and closing a round from the operator page, instead of through a
	// commit and a Flux reconcile. There is no admin check here on purpose: the
	// patch goes to Kubernetes with the caller's OWN token, so the answer comes
	// from RBAC. A participant holds get/list/watch on quizsessions and nothing
	// more, and the 403 the API server writes is the message the page shows --
	// the demo is better when the refusal is real.
	mux.HandleFunc("/public/rounds/{name}/state", requireParticipant(deps.cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var body struct {
			State string `json:"state"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "could not read the requested state"})
			return
		}
		// Only the two states an operator drives from a page. "draft" is the
		// presenter's scratch space and belongs in Git, where a round is
		// written; letting a button return a live round to it would hide it
		// from the room mid-demo with no record of who did it.
		if body.State != "live" && body.State != "closed" {
			writeJSON(w, 400, map[string]string{"error": `state must be "live" or "closed"`})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		patch := []byte(`{"spec":{"state":"` + body.State + `"}}`)
		updated, err := clients.dynamic.Resource(quizSessions).Namespace(deps.defaultNS).
			Patch(ctx, r.PathValue("name"), types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		writeJSON(w, 200, updated)
	}))
	mux.HandleFunc("/public/rounds/{name}/results", requireParticipant(deps.cfg, func(w http.ResponseWriter, r *http.Request, s participantSession) {
		noStore(w)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		clients, err := deps.participantClientsFor(s.IDToken)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		round, err := clients.dynamic.Resource(quizSessions).Namespace(deps.defaultNS).Get(ctx, r.PathValue("name"), metav1.GetOptions{})
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		spec, err := decodeQuizSpec(round)
		if err != nil {
			writeParticipantKubeError(w, err)
			return
		}
		results := make([]quizResult, len(spec.Questions))
		for i, q := range spec.Questions {
			results[i] = quizResult{Question: q, Choices: map[string]int{}, Text: []string{}}
		}
		total := 0
		options := metav1.ListOptions{LabelSelector: roundLabel + "=" + round.GetName(), Limit: 500}
		for {
			list, err := clients.dynamic.Resource(quizSubmissions).Namespace(deps.defaultNS).List(ctx, options)
			if err != nil {
				writeParticipantKubeError(w, err)
				return
			}
			for _, vote := range list.Items {
				var data struct {
					Answers []quizAnswer `json:"answers"`
				}
				raw, _ := json.Marshal(vote.Object["spec"])
				if json.Unmarshal(raw, &data) != nil || validateQuizAnswers(spec.Questions, data.Answers) != nil {
					continue
				}
				total++
				for _, a := range data.Answers {
					for i := range results {
						if results[i].Question.ID == a.QuestionID {
							results[i].add(a)
						}
					}
				}
			}
			if list.GetContinue() == "" {
				break
			}
			options.Continue = list.GetContinue()
		}
		writeJSON(w, 200, map[string]any{"round": round, "total": total, "questions": results})
	}))
}

type quizResult struct {
	Question quizQuestion   `json:"question"`
	Count    int            `json:"count"`
	Choices  map[string]int `json:"choices"`
	Sum      float64        `json:"sum"`
	Text     []string       `json:"text"`
}

func (r *quizResult) add(a quizAnswer) {
	r.Count++
	if a.SingleChoice != nil {
		r.Choices[*a.SingleChoice]++
	}
	if a.MultiChoice != nil {
		for _, c := range *a.MultiChoice {
			r.Choices[c]++
		}
	}
	if a.Number != nil {
		r.Sum += *a.Number
	}
	if a.FreeText != nil {
		r.Text = append(r.Text, *a.FreeText)
	}
}
