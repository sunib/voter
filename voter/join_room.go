package main

// GET /join-room?code=K7Q2 -- where the presenter's QR code points.
//
// A scanned code has to reach Room Pass's join page, so the participant only
// has to choose a display name. It cannot travel through the login: Room Pass
// builds its join URL itself, after Dex, from a handoff id nobody else sees,
// and krm-foyer's /auth/login takes no such parameter. So this endpoint puts
// the code where Room Pass looks for it, in its join cookie, and then starts
// krm-foyer's login through the Room Pass connector.
//
// Room Pass publishes that cookie as a channel for exactly this, and the code
// below is a CONSUMER of it, not the design. The contract -- cookie name,
// accepted values, attributes, single use -- belongs to Room Pass and is
// documented in its docs/qr-join.md. Read that before changing anything here;
// a local tweak to the name or the attributes silently degrades the flow to
// typing the code, which is the correct failure but a confusing one to debug.
//
// This works only because Room Pass answers /join on THIS SAME HOST (JOIN_ORIGIN
// in the deployment; Traefik splits the paths). Move either service to its own
// host and the hand-off stops arriving, by design.

import (
	"net/http"
	"strings"
	"time"
)

// joinRoomLogin is where /join-room sends the browser: krm-foyer's login, back
// to the front page, through Room Pass. Fixed here, never taken from the
// request, so this endpoint cannot be made into a redirect anywhere else.
const joinRoomLogin = "/auth/login?return_to=%2F&oidc.connector_id=room-pass"

func registerJoinRoomHandler(mux *http.ServeMux) {
	mux.HandleFunc("/join-room", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		noStore(w)
		// A value that was never a code is refused, not passed on: Room Pass
		// would ignore it, and the participant would be asked to type a code
		// with no hint why the scan did not count.
		code := safeJoinCode(r.URL.Query().Get("code"))
		if code == "" {
			http.Error(w, "not a room code", http.StatusBadRequest)
			return
		}
		setJoinCodeHandoff(w, code)
		http.Redirect(w, r, joinRoomLogin, http.StatusFound)
	})
}

// joinCodeHandoffCookie is the name Room Pass reads. Keep it in step with the
// constant of the same name in internal/server of sunib/room-pass -- the two
// projects cannot share it, so this is a contract kept by tests and
// documentation. Room Pass 2.0.0 kept the name.
//
// It is deliberately a PLAIN cookie, not a signed one. The value is a room
// code, which is untrusted input however it arrives: Room Pass checks it
// against the Room's currently valid codes whether it was typed, pasted or
// scanned, and presenting one confers exactly what typing one confers. Signing
// it here would imply this service vouches for it. It does not, and it holds
// none of Room Pass's keys to sign it with anyway.
const joinCodeHandoffCookie = "__Host-room-pass-joincode"

// joinCodeHandoffLifetime outlives the code itself on purpose. Codes are valid
// for seconds; a cookie that lingers a little longer can only ever produce the
// ordinary "that code is invalid" answer from Room Pass, whereas one that
// expires too early produces an empty field and a confused participant.
const joinCodeHandoffLifetime = 5 * time.Minute

// maxJoinCodeLength matches the Room CRD's upper bound for joinCode.length.
const maxJoinCodeLength = 12

// safeJoinCode bounds what may be placed into a Set-Cookie header, and nothing
// more. It is NOT a validity check: Room Pass owns that, against the Room's
// rotating status, and this service has no way to second-guess it.
//
// Deliberately looser than Room Pass's generator alphabet. Duplicating that
// alphabet here would mean a future change to it silently breaking scanned
// logins in a different module; "uppercase letters and digits, bounded" stays
// correct either way.
func safeJoinCode(raw string) string {
	value := strings.ToUpper(strings.TrimSpace(raw))
	// Room Pass normalises a typed "BCD-FGH" the same way, so a presenter who
	// hyphenates for legibility gets a QR that still works.
	value = strings.ReplaceAll(value, "-", "")
	if value == "" || len(value) > maxJoinCodeLength {
		return ""
	}
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return value
}

// setJoinCodeHandoff forwards a scanned code to Room Pass, or does nothing at
// all when the login did not come from a QR code.
func setJoinCodeHandoff(w http.ResponseWriter, raw string) {
	code := safeJoinCode(raw)
	if code == "" {
		return
	}
	// Never logged. A room code is short-lived, but it is still the thing that
	// lets someone into the room, and application logs outlive it.
	http.SetCookie(w, &http.Cookie{
		Name:     joinCodeHandoffCookie,
		Value:    code,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		// Lax, not Strict. The browser reaches /join by following a redirect
		// chain that passes through the issuer's host, so the request that
		// needs this cookie is a cross-site top-level GET navigation --
		// exactly what Lax allows and Strict drops.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(joinCodeHandoffLifetime.Seconds()),
	})
}
