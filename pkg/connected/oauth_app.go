package connected

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"
)

// Sign-ins that an app starts.
//
// The app is signed in to Fylvo, the system browser it sends the user to is not. What the
// app can hand to the browser is a link, and a link can be passed on. So the sign-in is
// tied together in steps:
//
//   - The app gets a ticket for its account, with the hash of a secret it keeps (the
//     challenge of its verifier).
//   - The first browser that opens the ticket, within a minute, owns it: it gets a
//     cookie whose hash is kept with the ticket. It is shown a page that names the Fylvo
//     account. Only a press there, from that browser and that page, makes the state at
//     the provider and sets the cookie of the sign-in, as the web flow does.
//   - What comes back from the provider is kept for the account, with the challenge. It
//     is handed out only to that account and only with the verifier.

const (
	// ticketLife is how long a ticket can be turned into a sign-in at the provider.
	ticketLife = 3 * time.Minute
	// ticketOpen is how long the app's browser has to open a ticket for the first time.
	ticketOpen = time.Minute
	// receiptLife is how long a finished sign-in of an app can be asked for again.
	receiptLife = 5 * time.Minute
	// appState starts the state of a sign-in from an app. No state of the web flow can
	// contain the dot, so the kind of a sign-in shows also when its state is gone.
	appState = "a."
)

// EnvAppSignin is the switch for sign-ins from apps. They are on unless the operator
// sets the variable to "0".
const EnvAppSignin = "FYLVO_CONNECTED_APP_SIGNIN"

// AppSignin tells whether an app can connect storages through an installation whose
// first address is primary, with a call that came under the address current. Both have
// to be https: the app sends its token and its verifier there, and the cookies of the
// flow are set for the first address. The one exception is a development build that
// plays the provider itself, so the flow can be tried on an emulator.
func AppSignin(primary, current *url.URL) bool {
	if os.Getenv(EnvAppSignin) == "0" {
		return false
	}
	if DevHandler() != nil {
		return true
	}
	return primary.Scheme == "https" && current.Scheme == "https"
}

var (
	// ErrFlowVerifier: the verifier of a sign-in from an app is missing or wrong, or one
	// was sent for a sign-in from the web app.
	ErrFlowVerifier = errors.New("the verifier of the sign-in is missing or wrong")
	// ErrFlowOvertaken: the storage was connected again by another sign-in meanwhile.
	ErrFlowOvertaken = errors.New("the storage was connected again by another sign-in")
)

// flowTicket is a sign-in an app asked for that has not gone to the provider yet, and
// after that the record of where it went, for a second press of the same button.
type flowTicket struct {
	userID    int
	provider  string
	reconnect *Reconnect
	challenge string
	attempt   string
	made      time.Time
	// opened is when a browser first asked for the ticket; browser is the hash of the
	// cookie that browser got.
	opened  time.Time
	browser [32]byte
	// state is the state at the provider the ticket became, address and signIn the
	// consent address and the sign-in cookie it was answered with.
	state   string
	address string
	signIn  string
}

// receipt remembers a finished sign-in of an app, so a finish whose answer was lost can
// be asked again. It holds no token.
type receipt struct {
	userID    int
	challenge string
	storage   string
	expires   time.Time
}

// TicketView is what is known of a ticket: whom it is for and what it connects.
type TicketView struct {
	UserID    int
	Provider  string
	Reconnect *Reconnect
	// Attempt is the id the app knows the sign-in by. It is no secret.
	Attempt string
	// Used tells that the ticket became a sign-in at the provider already.
	Used bool
}

func (t *flowTicket) view() *TicketView {
	return &TicketView{UserID: t.userID, Provider: t.provider, Reconnect: t.reconnect, Attempt: t.attempt, Used: t.state != ""}
}

// ValidChallenge tells whether a text has the form of a challenge: the base64url form
// of a SHA-256, without padding.
func ValidChallenge(challenge string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(challenge)
	return err == nil && len(raw) == sha256.Size && len(challenge) == 43
}

// challengeOf is the challenge of a verifier: SHA-256 over its text, as in PKCE "S256".
func challengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func sameHash(value string, hash [32]byte) bool {
	sum := sha256.Sum256([]byte(value))
	return value != "" && subtle.ConstantTimeCompare(sum[:], hash[:]) == 1
}

// Ticket keeps a sign-in an app asked for and returns its ticket and attempt id. It
// takes one of the places for sign-ins under way of the account.
func (f *Flow) Ticket(userID int, provider string, reconnect *Reconnect, challenge string) (ticket, attempt string, err error) {
	if f.specs[provider] == nil {
		return "", "", errors.New("unknown provider")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	if err := f.place(userID); err != nil {
		return "", "", err
	}
	ticket, attempt = randomToken(32), randomToken(12)
	f.tickets[ticket] = &flowTicket{
		userID: userID, provider: provider, reconnect: reconnect, challenge: challenge,
		attempt: attempt, made: f.now(),
	}
	return ticket, attempt, nil
}

// Open answers a browser that asks for a ticket. The first one, within ticketOpen, gets
// the ticket for itself: confirm is the value of the cookie to set in it. After that
// only the browser that shows this cookie is answered; every other one gets
// ErrFlowExpired, like for a ticket that is unknown, ran out, or became a sign-in that
// is over.
func (f *Flow) Open(ticket, cookie string) (view *TicketView, confirm string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	t := f.tickets[ticket]
	if t == nil {
		return nil, "", ErrFlowExpired
	}
	if t.opened.IsZero() {
		if f.now().After(t.made.Add(ticketOpen)) {
			delete(f.tickets, ticket)
			return nil, "", ErrFlowExpired
		}
		confirm = randomToken(32)
		t.opened, t.browser = f.now(), sha256.Sum256([]byte(confirm))
		return t.view(), confirm, nil
	}
	if !sameHash(cookie, t.browser) {
		return nil, "", ErrFlowExpired
	}
	return t.view(), "", nil
}

// Check tells what a ticket is for, to the browser that owns it.
func (f *Flow) Check(ticket, cookie string) (*TicketView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	t := f.tickets[ticket]
	if t == nil || t.opened.IsZero() || !sameHash(cookie, t.browser) {
		return nil, ErrFlowExpired
	}
	return t.view(), nil
}

// Drop uses a ticket up without a sign-in.
func (f *Flow) Drop(ticket string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.tickets, ticket)
}

// Start turns a ticket into a sign-in at the provider, for the browser that owns it. It
// returns the consent address and the value of the sign-in cookie for that browser. The
// state takes the place the ticket had. Asked again while that state lives, it answers
// the same again and makes nothing new.
func (f *Flow) Start(ticket, cookie string, client Client) (address, signIn string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	t := f.tickets[ticket]
	if t == nil || t.opened.IsZero() || !sameHash(cookie, t.browser) {
		return "", "", ErrFlowExpired
	}
	if t.state != "" {
		// sweep dropped the ticket if its state were over.
		return t.address, t.signIn, nil
	}
	spec := f.specs[t.provider]
	if spec == nil {
		return "", "", ErrFlowExpired
	}
	if f.revoking[t.provider] > 0 {
		return "", "", ErrFlowWait
	}
	state, verifier, signIn := appState+randomToken(24), randomToken(32), randomToken(32)
	f.states[state] = &flowState{
		userID: t.userID, provider: t.provider, verifier: verifier, browser: sha256.Sum256([]byte(signIn)),
		redirect: client.Redirect, reconnect: t.reconnect, expires: f.now().Add(stateLife),
		clientID: client.ID, clientAuth: client.Secret,
		app: true, challenge: t.challenge, attempt: t.attempt, made: t.made,
	}
	t.state, t.address, t.signIn = state, f.consent(spec, client, state, verifier, true), signIn
	return t.address, t.signIn, nil
}

// IsAppState tells whether a state value is one of a sign-in from an app.
func IsAppState(state string) bool {
	return strings.HasPrefix(state, appState)
}

// take finds the result of a sign-in for the account that started it. A result of a
// sign-in from an app needs the verifier of that app; one from the web app must come
// without. remove takes it out.
func (f *Flow) take(userID int, code, verifier string, remove bool) (*Granted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	g := f.granted[code]
	if g == nil {
		return nil, ErrFlowExpired
	}
	if g.UserID != userID {
		// Left in place: the user who started the sign-in can still finish it.
		return nil, ErrFlowAccount
	}
	if g.App != (verifier != "") {
		return nil, ErrFlowVerifier
	}
	if g.App && subtle.ConstantTimeCompare([]byte(challengeOf(verifier)), []byte(g.Challenge)) != 1 {
		return nil, ErrFlowVerifier
	}
	if remove {
		delete(f.granted, code)
	}
	return g, nil
}

// Peek shows the result of a sign-in from an app to the account and the verifier it is
// for, and leaves it where it is.
func (f *Flow) Peek(userID int, code, verifier string) (*Granted, error) {
	g, err := f.take(userID, code, verifier, false)
	if err == nil && !g.App {
		return nil, ErrFlowVerifier
	}
	return g, err
}

// Cancel drops the result of a sign-in from an app, for the account and the verifier it
// is for. A result of the web app is never dropped this way.
func (f *Flow) Cancel(userID int, code, verifier string) error {
	if verifier == "" {
		if _, err := f.take(userID, code, "", false); err != nil {
			return err
		}
		return ErrFlowVerifier
	}
	_, err := f.take(userID, code, verifier, true)
	return err
}

// Remember keeps a receipt for a stored sign-in of an app.
func (f *Flow) Remember(code string, g *Granted, storage string) {
	if !g.App {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.receipts[code] = &receipt{userID: g.UserID, challenge: g.Challenge, storage: storage, expires: f.now().Add(receiptLife)}
}

// Receipt returns the storage a sign-in of an app was stored as, to the account and the
// verifier it was stored for.
func (f *Flow) Receipt(userID int, code, verifier string) (storage string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	r := f.receipts[code]
	if r == nil || r.userID != userID || verifier == "" ||
		subtle.ConstantTimeCompare([]byte(challengeOf(verifier)), []byte(r.challenge)) != 1 {
		return "", false
	}
	return r.storage, true
}
