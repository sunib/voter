package main

// Counting a round, once.
//
// There used to be one counter, inside the results HTTP handler. There are now
// two readers -- that handler and the reconciler in quiz_reconciler.go, which
// writes the same numbers into QuizSession.status -- and two counters would be
// two answers to the same question, differing exactly where it hurts most: on
// the ballot that fails validation. So the counting lives here and both call
// it. docs/live-results-design.md calls this the property the whole design
// buys; quiz_tally_test.go pins it.

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// statusTextSample bounds the free text kept in status, and nothing else.
//
// Counts stay exact however many people vote. Text cannot: 300 answers of up to
// 2000 characters is ~600 KB in an object whose etcd ceiling is about 1.5 MB,
// rewritten on every tally. The REST endpoint keeps returning all of them, so
// nothing is lost -- and a projector showing 300 free-text answers was already
// a wall the presenter reads two or three lines off.
//
// It is also maxItems on status.questions.text in config/crd/quizsessions.yaml.
// Raise one and the API server rejects every tally until the other follows.
const statusTextSample = 25

// ballotRound is what a submission is a vote IN, and the only thing that
// selects it.
//
// This used to be the voter.configbutler.ai/round LABEL, which the application
// always sets and the CRD does not require -- so a ballot pasted with kubectl
// without it was accepted by the API server, mirrored to Git, and counted by
// nobody. spec.sessionRef is required, typed, and the object's own statement of
// what it is. The label stays for the Git mirror's folder layout and for the
// reset; it stops being load bearing.
func ballotRound(ballot *unstructured.Unstructured) string {
	name, _, _ := unstructured.NestedString(ballot.Object, "spec", "sessionRef", "name")
	return name
}

func ballotSubmittedAt(ballot *unstructured.Unstructured) time.Time {
	raw, _, _ := unstructured.NestedString(ballot.Object, "spec", "submittedAt")
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return at
}

// quizTally is one round's result: how many ballots carry its name, how many of
// those counted, and the per-question numbers.
//
// filed and counted differ when a ballot fails validateQuizAnswers -- a
// hand-written one naming a question id that has since been edited, which is
// exactly what "Casting your own answers" risks. Showing both makes a dropped
// vote visible instead of silent.
type quizTally struct {
	Filed     int
	Counted   int
	Questions []quizResult
}

// tallyRound counts every ballot cast in round, whoever cast it.
//
// ballots is the whole namespace; the filter is here rather than in a label
// selector so there is one selection rule and both readers obey it.
func tallyRound(round string, questions []quizQuestion, ballots []*unstructured.Unstructured) quizTally {
	results := make([]quizResult, len(questions))
	for i, q := range questions {
		results[i] = quizResult{Question: q, Choices: map[string]int{}, Text: []string{}}
	}
	cast := make([]*unstructured.Unstructured, 0, len(ballots))
	for _, ballot := range ballots {
		if ballot != nil && ballotRound(ballot) == round {
			cast = append(cast, ballot)
		}
	}
	// Oldest first. Two things rest on the order: "the most recent 25" in
	// status is then a tail, and the room reads free text in the order it was
	// written rather than in whatever order the API server paginated.
	slices.SortFunc(cast, func(a, b *unstructured.Unstructured) int {
		if c := ballotSubmittedAt(a).Compare(ballotSubmittedAt(b)); c != 0 {
			return c
		}
		return strings.Compare(a.GetName(), b.GetName())
	})
	tally := quizTally{Filed: len(cast), Questions: results}
	for _, ballot := range cast {
		var data struct {
			Answers []quizAnswer `json:"answers"`
		}
		raw, _ := json.Marshal(ballot.Object["spec"])
		if json.Unmarshal(raw, &data) != nil || validateQuizAnswers(questions, data.Answers) != nil {
			continue
		}
		tally.Counted++
		for _, a := range data.Answers {
			for i := range results {
				if results[i].Question.ID == a.QuestionID {
					results[i].add(a)
				}
			}
		}
	}
	return tally
}

// The shape written to QuizSession.status, and the schema in
// config/crd/quizsessions.yaml has to match it field for field.
type quizStatusQuestion struct {
	ID      string         `json:"id"`
	Count   int            `json:"count"`
	Choices map[string]int `json:"choices"`
	Sum     float64        `json:"sum"`
	// How many free-text answers were written, against the bounded sample below.
	TextTotal int      `json:"textTotal"`
	Text      []string `json:"text"`
}

type quizStatus struct {
	// Which spec this tally describes. With the status subresource restored,
	// metadata.generation moves only when the questions change, so a tally whose
	// observedGeneration lags was computed against questions that have since
	// been edited.
	ObservedGeneration int64 `json:"observedGeneration"`
	// Rendered on screen, because a controller that has stopped looks exactly
	// like a room that has stopped voting and the timestamp is the only thing
	// that tells them apart.
	LastTallyTime string               `json:"lastTallyTime"`
	Filed         int                  `json:"filed"`
	Counted       int                  `json:"counted"`
	Questions     []quizStatusQuestion `json:"questions"`
}

func (t quizTally) status(generation int64, at time.Time) quizStatus {
	questions := make([]quizStatusQuestion, len(t.Questions))
	for i, r := range t.Questions {
		sample := r.Text
		if len(sample) > statusTextSample {
			sample = sample[len(sample)-statusTextSample:]
		}
		questions[i] = quizStatusQuestion{
			ID:        r.Question.ID,
			Count:     r.Count,
			Choices:   r.Choices,
			Sum:       r.Sum,
			TextTotal: len(r.Text),
			Text:      append([]string{}, sample...),
		}
	}
	return quizStatus{
		ObservedGeneration: generation,
		LastTallyTime:      at.UTC().Format(time.RFC3339),
		Filed:              t.Filed,
		Counted:            t.Counted,
		Questions:          questions,
	}
}
