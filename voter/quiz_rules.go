package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
// docs/deliberate-simplifications.md. Staleness is unaffected: admission
// (config/admission/quizsubmission-policy.yaml) requires a participant's ballot
// to pin the round's uid and questions digest, which catches a recreated round
// as surely as an edited one, and the tally checks the same pins -- see
// ballotPinned.
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
