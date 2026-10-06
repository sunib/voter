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
	"crypto/sha256"
	"encoding/hex"
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

// questionsDigest names a round's questions, so a ballot can say which ones it
// answered.
//
// Not metadata.generation. state lives in the round's spec, so opening and
// closing a round each move the generation: a ballot that pinned it would stop
// matching the moment the round closed, which is exactly when it is counted.
// The vote handler gets away with comparing generations because it does so at
// vote time; anything checked later -- this tally, an admission policy reading
// the round -- needs a name for the questions alone.
//
// The digest is computed here, from the decoded questions, and published in the
// round's status. A ballot copies that value rather than computing its own, so
// no second implementation has to reproduce Go's JSON encoding byte for byte.
func questionsDigest(questions []quizQuestion) string {
	encoded, _ := json.Marshal(questions)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ballotPinned reports whether a ballot was cast against this very round and
// these very questions -- as far as it says.
//
// The two pins are spec.roundUID and spec.questionsDigest. The vote handler
// writes both. A ballot without them is counted as before: the "Casting your
// own answers" interlude pastes ballots by hand, and a pasted ballot has no
// reason to know a UID. Each pin that IS present has to match, so a ballot for
// a deleted round never counts for its same-named successor, and one cast
// against since-edited questions is filed but not counted.
func ballotPinned(ballot *unstructured.Unstructured, roundUID, digest string) bool {
	uid, hasUID, _ := unstructured.NestedString(ballot.Object, "spec", "roundUID")
	pinned, hasDigest, _ := unstructured.NestedString(ballot.Object, "spec", "questionsDigest")
	return (!hasUID || uid == roundUID) && (!hasDigest || pinned == digest)
}

// quizTally is one round's result: how many ballots carry its name, how many of
// those counted, and the per-question numbers.
//
// filed and counted differ when a ballot fails validateQuizAnswers -- a
// hand-written one naming a question id that has since been edited, which is
// exactly what "Casting your own answers" risks -- or when its pins name another
// round or other questions. Showing both makes a dropped vote visible instead of
// silent.
type quizTally struct {
	Filed     int
	Counted   int
	Questions []quizResult
	// The digest the pins were checked against, published in status for the
	// next ballot to copy.
	QuestionsDigest string
}

// tallyRound counts every ballot cast in round, whoever cast it.
//
// ballots is the whole namespace; the filter is here rather than in a label
// selector so there is one selection rule and both readers obey it.
func tallyRound(roundObj *unstructured.Unstructured, questions []quizQuestion, ballots []*unstructured.Unstructured) quizTally {
	round, roundUID, digest := roundObj.GetName(), string(roundObj.GetUID()), questionsDigest(questions)
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
	tally := quizTally{Filed: len(cast), Questions: results, QuestionsDigest: digest}
	for _, ballot := range cast {
		if !ballotPinned(ballot, roundUID, digest) {
			continue
		}
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
	// What a ballot copies into spec.questionsDigest. See questionsDigest.
	QuestionsDigest string `json:"questionsDigest"`
	// When Voter SAW the round open and close: see roundTimes. Pointers without
	// omitempty, so that nil is written as null and a merge patch removes the
	// field -- which is how a reopened round loses its closedAt.
	OpenedAt *string `json:"openedAt"`
	ClosedAt *string `json:"closedAt"`
}

// roundTimes carries a round's openedAt and closedAt forward from its stored
// status, moving them on what the round's state is now.
//
// openedAt is set the first time the round is seen live and then kept, through
// a close and a reopen, because reopening keeps the votes already cast.
// closedAt is set when the round is seen closed and cleared when it is seen
// live again.
//
// Both are when VOTER OBSERVED the change, not when it was made. Voter learns a
// round's state by watching it, so a change made while Voter is down -- an image
// bump rolls it mid-round -- is stamped on the way back up. That is why they are
// status for people to read and are not a counting rule: a rule of "created
// after openedAt" would drop every vote cast before such a restart. Refusing a
// ballot outside the live window belongs at the moment it is created, which is
// the vote handler today and an admission policy next
// (docs/quiz-admission.md).
func roundTimes(state string, stored *unstructured.Unstructured, at time.Time) (opened, closed *string) {
	carried := func(field string) *string {
		value, found, _ := unstructured.NestedString(stored.Object, "status", field)
		if !found || value == "" {
			return nil
		}
		return &value
	}
	now := at.UTC().Format(time.RFC3339)
	opened, closed = carried("openedAt"), carried("closedAt")
	switch state {
	case "live":
		if opened == nil {
			opened = &now
		}
		closed = nil
	case "closed":
		if closed == nil {
			closed = &now
		}
	}
	return opened, closed
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
		QuestionsDigest:    t.QuestionsDigest,
	}
}
