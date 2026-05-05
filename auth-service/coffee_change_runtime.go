package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type coffeeChangeRuntime struct {
	mu           sync.Mutex
	maxEntries   int
	nextChangeID int
	changes      []coffeeConfigChangeRecord
	subscribers  map[int]chan coffeeConfigChangeRecord
	nextSubID    int
}

func newCoffeeChangeRuntime(maxEntries int) *coffeeChangeRuntime {
	if maxEntries <= 0 {
		maxEntries = 64
	}
	return &coffeeChangeRuntime{
		maxEntries:  maxEntries,
		subscribers: map[int]chan coffeeConfigChangeRecord{},
	}
}

func (r *coffeeChangeRuntime) record(record coffeeConfigChangeRecord) coffeeConfigChangeRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextChangeID++
	record.ID = fmt.Sprintf("coffee-config-%06d", r.nextChangeID)
	record.Changes = append([]coffeeConfigFieldChange(nil), record.Changes...)

	r.changes = append(r.changes, record)
	if len(r.changes) > r.maxEntries {
		r.changes = append([]coffeeConfigChangeRecord(nil), r.changes[len(r.changes)-r.maxEntries:]...)
	}

	for _, ch := range r.subscribers {
		select {
		case ch <- record:
		default:
		}
	}

	return record
}

func (r *coffeeChangeRuntime) snapshot() coffeeConfigChangesSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	changes := make([]coffeeConfigChangeRecord, 0, len(r.changes))
	for index := len(r.changes) - 1; index >= 0; index-- {
		changes = append(changes, cloneChangeRecord(r.changes[index]))
	}
	return coffeeConfigChangesSnapshot{Changes: changes}
}

func (r *coffeeChangeRuntime) subscribe(buffer int) (int, <-chan coffeeConfigChangeRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextSubID++
	id := r.nextSubID
	ch := make(chan coffeeConfigChangeRecord, buffer)
	r.subscribers[id] = ch
	return id, ch
}

func (r *coffeeChangeRuntime) unsubscribe(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch, ok := r.subscribers[id]
	if !ok {
		return
	}
	delete(r.subscribers, id)
	close(ch)
}

func newCoffeeConfigChange(now time.Time, before, after coffeeConfig, patchBody []byte, actor, reason string) coffeeConfigChangeRecord {
	changes := collectCoffeeConfigChanges(before, after, patchBody)
	return coffeeConfigChangeRecord{
		CreatedAt:  now.UTC().Format(time.RFC3339),
		Actor:      normalizeAdminActor(actor),
		Reason:     strings.TrimSpace(reason),
		Summary:    summarizeCoffeeConfigChanges(changes),
		Generation: after.Metadata.Generation,
		Changes:    changes,
	}
}

func collectCoffeeConfigChanges(before, after coffeeConfig, patchBody []byte) []coffeeConfigFieldChange {
	beforeObject := toJSONObject(before)
	afterObject := toJSONObject(after)
	if beforeObject == nil || afterObject == nil {
		return []coffeeConfigFieldChange{{
			Path:          "spec",
			PreviousValue: before.Spec,
			NewValue:      after.Spec,
		}}
	}

	var patchObject map[string]any
	if err := json.Unmarshal(patchBody, &patchObject); err != nil || len(patchObject) == 0 {
		return []coffeeConfigFieldChange{{
			Path:          "spec",
			PreviousValue: before.Spec,
			NewValue:      after.Spec,
		}}
	}

	changes := collectCoffeeConfigChangesFromPatch("", patchObject, beforeObject, afterObject)
	if len(changes) == 0 && !reflect.DeepEqual(before.Spec, after.Spec) {
		return []coffeeConfigFieldChange{{
			Path:          "spec",
			PreviousValue: before.Spec,
			NewValue:      after.Spec,
		}}
	}
	return changes
}

func collectCoffeeConfigChangesFromPatch(path string, patchValue any, beforeRoot, afterRoot map[string]any) []coffeeConfigFieldChange {
	beforeValue := lookupJSONPath(beforeRoot, path)
	afterValue := lookupJSONPath(afterRoot, path)

	objectPatch, ok := patchValue.(map[string]any)
	if !ok {
		if path == "" || reflect.DeepEqual(beforeValue, afterValue) {
			return nil
		}
		return []coffeeConfigFieldChange{{
			Path:          path,
			PreviousValue: cloneJSONValue(beforeValue),
			NewValue:      cloneJSONValue(afterValue),
		}}
	}

	keys := make([]string, 0, len(objectPatch))
	for key := range objectPatch {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	changes := make([]coffeeConfigFieldChange, 0, len(keys))
	for _, key := range keys {
		changes = append(changes, collectCoffeeConfigChangesFromPatch(joinJSONPath(path, key), objectPatch[key], beforeRoot, afterRoot)...)
	}

	if len(changes) == 0 && path != "" && !reflect.DeepEqual(beforeValue, afterValue) {
		return []coffeeConfigFieldChange{{
			Path:          path,
			PreviousValue: cloneJSONValue(beforeValue),
			NewValue:      cloneJSONValue(afterValue),
		}}
	}
	return changes
}

func summarizeCoffeeConfigChanges(changes []coffeeConfigFieldChange) string {
	switch len(changes) {
	case 0:
		return "Updated coffee config"
	case 1:
		return fmt.Sprintf("Updated %s", humanizeChangePath(changes[0].Path))
	default:
		return fmt.Sprintf("Updated %d fields", len(changes))
	}
}

func humanizeChangePath(path string) string {
	label := strings.TrimPrefix(strings.TrimSpace(path), "spec.")
	if label == "" {
		return "coffee config"
	}
	label = strings.ReplaceAll(label, ".apiKeySecretRef.", " secret ")
	label = strings.ReplaceAll(label, ".orderConfirmationTemplate", " mail template")
	label = strings.ReplaceAll(label, ".zeroAmountCheckoutAllowed", " zero checkout")
	label = strings.ReplaceAll(label, ".", " / ")
	return label
}

func normalizeAdminActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "Unknown admin"
	}
	return actor
}

func toJSONObject(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil
	}
	return out
}

func lookupJSONPath(root map[string]any, path string) any {
	if root == nil || path == "" {
		return root
	}

	current := any(root)
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[segment]
	}
	return current
}

func joinJSONPath(base, key string) string {
	if base == "" {
		return key
	}
	return base + "." + key
}

func cloneChangeRecord(record coffeeConfigChangeRecord) coffeeConfigChangeRecord {
	out := record
	out.Changes = append([]coffeeConfigFieldChange(nil), record.Changes...)
	return out
}

func cloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return value
	}
	return out
}
