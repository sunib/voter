package main

import (
	"context"
	"crypto/sha256"
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
)

var quizSessions = schema.GroupVersionResource{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "quizsessions"}
var quizSubmissions = schema.GroupVersionResource{Group: "examples.configbutler.ai", Version: "v1alpha1", Resource: "quizsubmissions"}

const roundLabel = "voter.configbutler.ai/round-uid"

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
			writeJSON(w, 200, round)
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
		var req struct {
			UID             string       `json:"uid"`
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
		if req.UID == "" || req.UID != string(round.GetUID()) || req.ResourceVersion != round.GetResourceVersion() {
			writeJSON(w, 409, map[string]string{"error": "The round changed. Reload the questions before voting."})
			return
		}
		if err := validateQuizAnswers(spec.Questions, req.Answers); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		// Atomic create makes retries/concurrent tabs one vote per enrolled identity
		// and round UID. Recreating a round starts fresh; reopening it does not.
		hash := sha256.Sum256([]byte(req.UID + "\x00" + s.Subject))
		name := fmt.Sprintf("vote-%x", hash[:])
		answers, _ := json.Marshal(req.Answers)
		var answerObjects []any
		_ = json.Unmarshal(answers, &answerObjects)
		if answerObjects == nil {
			answerObjects = []any{}
		}
		obj := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "examples.configbutler.ai/v1alpha1", "kind": "QuizSubmission",
			"metadata": map[string]any{"name": name, "namespace": deps.defaultNS, "labels": map[string]any{roundLabel: req.UID}},
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
		options := metav1.ListOptions{LabelSelector: roundLabel + "=" + string(round.GetUID()), Limit: 500}
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
