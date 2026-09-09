// Package v1alpha1 defines the Room Pass enrollment API.
// +kubebuilder:object:generate=true
// +groupName=roompass.configbutler.ai
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "roompass.configbutler.ai", Version: "v1alpha1"}

func AddToScheme(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &Room{}, &RoomList{}, &Participant{}, &ParticipantList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}

// JoinCodeSpec controls rotation and the grace period for typing a previous code.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="joinCode is immutable"
// +kubebuilder:validation:XValidation:rule="duration(self.validFor) >= duration(self.rotateEvery) + duration('5s') && duration(self.validFor) <= duration(self.rotateEvery) + duration(self.rotateEvery) + duration(self.rotateEvery) + duration(self.rotateEvery)",message="validFor must provide at least five seconds overlap and retain at most four codes"
type JoinCodeSpec struct {
	// +kubebuilder:default="15s"
	// +kubebuilder:validation:Enum="10s";"15s";"30s";"60s"
	RotateEvery string `json:"rotateEvery,omitempty"`
	// +kubebuilder:default="30s"
	// +kubebuilder:validation:Enum="20s";"30s";"45s";"60s";"90s";"120s";"180s";"240s"
	ValidFor string `json:"validFor,omitempty"`
	// +kubebuilder:default=6
	// +kubebuilder:validation:Minimum=6
	// +kubebuilder:validation:Maximum=12
	Length int `json:"length,omitempty"`
}

type RoomSpec struct {
	// Title appears on the participant join page.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=120
	Title string `json:"title"`
	// EndsAt can be extended without changing participant identities.
	EndsAt metav1.Time `json:"endsAt"`
	// +kubebuilder:default=Closed
	// +kubebuilder:validation:Enum=Open;Closed
	Enrollment string `json:"enrollment"`
	// Stopped permanently denies enrollment and new identity assertions.
	// +kubebuilder:default=false
	// +kubebuilder:validation:XValidation:rule="!oldSelf || self",message="stopping is irreversible"
	Stopped bool `json:"stopped"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	MaxParticipants int `json:"maxParticipants"`
	// AudienceGroup selects operator-managed RBAC; Room Pass never creates grants.
	// +kubebuilder:validation:Pattern="^demo:[a-zA-Z0-9][a-zA-Z0-9:_-]*$"
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="audienceGroup is immutable"
	AudienceGroup string `json:"audienceGroup"`
	// AllowedReturnURLs must also appear in the deployment allowlist.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=2048
	// +kubebuilder:validation:items:Pattern="^https://[^/?#@]+/[^#]*$"
	// +listType=set
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="allowedReturnURLs is immutable"
	AllowedReturnURLs []string `json:"allowedReturnURLs"`
	// +kubebuilder:default={rotateEvery:"15s",validFor:"30s",length:6}
	JoinCode JoinCodeSpec `json:"joinCode"`
}

type Code struct {
	// +kubebuilder:validation:MaxLength=12
	Code      string      `json:"code"`
	IssuedAt  metav1.Time `json:"issuedAt"`
	ExpiresAt metav1.Time `json:"expiresAt"`
}
type RoomStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	JoinCode   *Code              `json:"joinCode,omitempty"`
	// ValidJoinCodes is operator-only credential material, never a public response.
	// +kubebuilder:validation:MaxItems=4
	ValidJoinCodes   []Code `json:"validJoinCodes,omitempty"`
	ParticipantCount int    `json:"participantCount"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Enrollment",type=string,JSONPath=`.spec.enrollment`
// +kubebuilder:printcolumn:name="Participants",type=integer,JSONPath=`.status.participantCount`
// +kubebuilder:printcolumn:name="Ends",type=date,JSONPath=`.spec.endsAt`
type Room struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RoomSpec   `json:"spec"`
	Status            RoomStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type RoomList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Room `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="roomRef is immutable"
type RoomRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// UID prevents name reuse from reviving enrollment.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
}
type ParticipantSpec struct {
	RoomRef RoomRef `json:"roomRef"`
	// DisplayName is an unverified demo label, never an authorization key.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern="^[^<>\\x00-\\x1f\\x7f]+$"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="displayName is immutable"
	DisplayName string `json:"displayName"`
	// +kubebuilder:default=false
	// +kubebuilder:validation:XValidation:rule="!oldSelf || self",message="revocation is irreversible"
	Revoked bool `json:"revoked"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Room",type=string,JSONPath=`.spec.roomRef.name`
// +kubebuilder:printcolumn:name="Revoked",type=boolean,JSONPath=`.spec.revoked`
type Participant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ParticipantSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type ParticipantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Participant `json:"items"`
}
