package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestQuizAnswerValidation(t *testing.T) {
	var questions []quizQuestion
	if err := json.Unmarshal([]byte(`[{"id":"multi","title":"Multi","type":"multiChoice","choices":["a","b"],"required":true},{"id":"score","title":"Score","type":"scale0to10"},{"id":"n","title":"Number","type":"number","min":-2,"max":2},{"id":"text","title":"Text","type":"freeText","required":true}]`), &questions); err != nil {
		t.Fatal(err)
	}
	valid := `[{"questionId":"multi","multiChoice":["a","b"]},{"questionId":"score","number":0},{"questionId":"n","number":-2},{"questionId":"text","freeText":"ok"}]`
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"all types", valid, true},
		{"empty required", strings.Replace(valid, `["a","b"]`, `[]`, 1), false},
		{"duplicate choice", strings.Replace(valid, `["a","b"]`, `["a","a"]`, 1), false},
		{"range", strings.Replace(valid, `"number":0`, `"number":11`, 1), false},
		{"numeric min", strings.Replace(valid, `"number":-2`, `"number":-3`, 1), false},
		{"blank required", strings.Replace(valid, `"ok"`, `"  "`, 1), false},
		{"two fields", strings.Replace(valid, `"freeText":"ok"`, `"freeText":"ok","number":1`, 1), false},
		{"duplicate question", strings.Replace(valid, `"questionId":"n"`, `"questionId":"score"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var answers []quizAnswer
			if err := json.Unmarshal([]byte(tc.body), &answers); err != nil {
				t.Fatal(err)
			}
			err := validateQuizAnswers(questions, answers)
			if (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}
