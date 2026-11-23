package connected

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// Sign-ins that an app starts (see oauth_app.go).

const (
	// The test value of PKCE (RFC 7636, appendix B).
	appVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	appChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	// Another app's secret.
	otherVerifier = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

func TestAppChallenge(t *testing.T) {
	require.Equal(t, appChallenge, challengeOf(appVerifier))
	require.True(t, ValidChallenge(appChallenge))
	for _, bad := range []string{"", "abc", appChallenge + "A", appChallenge[:42], appChallenge[:42] + "=", strings.Repeat("!", 43)} {
		require.False(t, ValidChallenge(bad), bad)
	}
	// Another spelling of the same bytes: it decodes, and no verifier gives this text.
	require.True(t, strings.HasSuffix(appChallenge, "M"))
	require.False(t, ValidChallenge(appChallenge[:42]+"N"))
}

// appStart plays the app and its browser up to the consent address.
func appStart(t *testing.T, flow *Flow, client Client, userID int) (ticket, attempt, confirm, address, signIn string) {
	t.Helper()
	ticket, attempt, err := flow.Ticket(userID, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	_, confirm, err = flow.Open(ticket, "")
	require.NoError(t, err)
	require.NotEmpty(t, confirm)
	address, signIn, err = flow.Start(ticket, confirm, client)
	require.NoError(t, err)
	return ticket, attempt, confirm, address, signIn
}

func TestAppFlowHappyPath(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()

	ticket, attempt, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	require.Len(t, ticket, 43)
	require.Len(t, attempt, 16)
	// A ticket is nothing at the provider: taking tokens back does not wait for it.
	require.False(t, flow.Busy(ProviderDrive))
	require.Empty(t, flow.states)

	// The first browser gets the ticket for itself.
	view, confirm, err := flow.Open(ticket, "")
	require.NoError(t, err)
	require.NotEmpty(t, confirm)
	require.Equal(t, &TicketView{UserID: 7, Provider: ProviderDrive, Attempt: attempt}, view)
	// It gets the page again, and no second cookie.
	view, again, err := flow.Open(ticket, confirm)
	require.NoError(t, err)
	require.Empty(t, again)
	require.False(t, view.Used)

	address, signIn, err := flow.Start(ticket, confirm, client)
	require.NoError(t, err)
	require.NotEmpty(t, signIn)
	require.NotEqual(t, confirm, signIn)
	require.True(t, flow.Busy(ProviderDrive))
	state, code := p.consent(t, address)
	require.True(t, IsAppState(state))
	require.Len(t, flow.states, 1)
	require.NotContains(t, address, ticket)
	require.NotContains(t, address, appChallenge)

	out, err := flow.Callback(ctx, state, signIn, code, "")
	require.NoError(t, err)
	require.Equal(t, Outcome{
		Finish: out.Finish, App: true, Attempt: attempt, UserID: 7, Provider: ProviderDrive,
		Account: "user@example.com", AccountKind: AccountAddress,
	}, out)
	require.Len(t, out.Finish, 32)

	// The look before storing leaves the result where it is.
	for i := 0; i < 2; i++ {
		g, err := flow.Peek(7, out.Finish, appVerifier)
		require.NoError(t, err)
		require.Equal(t, "user@example.com", g.Identity.Shown)
	}
	g, err := flow.Finish(7, out.Finish, appVerifier)
	require.NoError(t, err)
	require.True(t, g.App)
	require.Equal(t, attempt, g.Attempt)
	_, err = flow.Finish(7, out.Finish, appVerifier)
	require.ErrorIs(t, err, ErrFlowExpired)
}

// The ticket belongs to the first browser, and only within a minute of the app's call.
func TestAppTicketBelongsToTheFirstBrowser(t *testing.T) {
	flow, _, client := testFlow(t)
	now := time.Unix(1800000000, 0)
	flow.now = func() time.Time { return now }

	ticket, _, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	// Nobody owns it yet: nothing to check, nothing to start.
	_, err = flow.Check(ticket, "")
	require.ErrorIs(t, err, ErrFlowExpired)
	_, _, err = flow.Start(ticket, "", client)
	require.ErrorIs(t, err, ErrFlowExpired)
	_, _, err = flow.Start(ticket, "made-up", client)
	require.ErrorIs(t, err, ErrFlowExpired)

	_, confirm, err := flow.Open(ticket, "")
	require.NoError(t, err)
	// A second browser, with and without a cookie of its own.
	for _, cookie := range []string{"", "made-up", confirm + "x"} {
		_, _, err = flow.Open(ticket, cookie)
		require.ErrorIs(t, err, ErrFlowExpired)
		_, err = flow.Check(ticket, cookie)
		require.ErrorIs(t, err, ErrFlowExpired)
		_, _, err = flow.Start(ticket, cookie, client)
		require.ErrorIs(t, err, ErrFlowExpired)
	}
	// The owner is not disturbed by that.
	address, signIn, err := flow.Start(ticket, confirm, client)
	require.NoError(t, err)

	// A double tap, or back and the button again: the same answer, nothing new.
	again, signInAgain, err := flow.Start(ticket, confirm, client)
	require.NoError(t, err)
	require.Equal(t, address, again)
	require.Equal(t, signIn, signInAgain)
	require.Len(t, flow.states, 1)
	view, _, err := flow.Open(ticket, confirm)
	require.NoError(t, err)
	require.True(t, view.Used)
	// Used or not, another browser gets nothing.
	_, _, err = flow.Start(ticket, "made-up", client)
	require.ErrorIs(t, err, ErrFlowExpired)

	// A ticket nobody opened within a minute is over, also for the first one who asks.
	late, _, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	now = now.Add(ticketOpen + time.Second)
	_, _, err = flow.Open(late, "")
	require.ErrorIs(t, err, ErrFlowExpired)
	require.Nil(t, flow.tickets[late])

	// One that was opened in time can be started until the ticket runs out.
	slow, _, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	_, slowConfirm, err := flow.Open(slow, "")
	require.NoError(t, err)
	now = now.Add(ticketLife - time.Second)
	_, err = flow.Check(slow, slowConfirm)
	require.NoError(t, err)
	now = now.Add(2 * time.Second)
	_, _, err = flow.Start(slow, slowConfirm, client)
	require.ErrorIs(t, err, ErrFlowExpired)
}

// A used ticket lives as long as the state it became, and not longer.
func TestAppTicketEndsWithItsState(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	flow.now = func() time.Time { return now }

	ticket, _, confirm, address, signIn := appStart(t, flow, client, 7)
	// Long after the ticket itself would have run out, the repeat still works.
	now = now.Add(ticketLife + time.Minute)
	again, _, err := flow.Start(ticket, confirm, client)
	require.NoError(t, err)
	require.Equal(t, address, again)

	state, code := p.consent(t, address)
	_, err = flow.Callback(ctx, state, signIn, code, "")
	require.NoError(t, err)
	// The state is used: the ticket is over, for its own browser too.
	_, _, err = flow.Start(ticket, confirm, client)
	require.ErrorIs(t, err, ErrFlowExpired)
	_, _, err = flow.Open(ticket, confirm)
	require.ErrorIs(t, err, ErrFlowExpired)
	require.Empty(t, flow.tickets)

	// The same when the state runs out.
	ticket, _, confirm, _, _ = appStart(t, flow, client, 7)
	now = now.Add(stateLife + time.Second)
	_, _, err = flow.Start(ticket, confirm, client)
	require.ErrorIs(t, err, ErrFlowExpired)
	require.Empty(t, flow.tickets)
	require.Empty(t, flow.states)
}

// The consent address in another browser: that one has no cookie of the sign-in. The
// state is used up, so the address is of no use to anybody afterwards.
func TestAppCallbackNeedsTheBrowserThatConfirmed(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()
	_, _, confirm, address, signIn := appStart(t, flow, client, 7)
	state, code := p.consent(t, address)

	// The confirmation cookie is not the cookie of the sign-in.
	for _, cookie := range []string{"", confirm, signIn + "x"} {
		_, _, _, address, _ := appStart(t, flow, client, 8)
		state, code := p.consent(t, address)
		out, err := flow.Callback(ctx, state, cookie, code, "")
		require.ErrorIs(t, err, ErrFlowBrowser)
		// Nothing about the attempt is told to a browser that proved nothing.
		require.Equal(t, Outcome{App: true}, out)
	}

	out, err := flow.Callback(ctx, state, "", code, "")
	require.ErrorIs(t, err, ErrFlowBrowser)
	require.Equal(t, Outcome{App: true}, out)
	// The right browser comes too late. That it was a sign-in of an app still shows.
	out, err = flow.Callback(ctx, state, signIn, code, "")
	require.ErrorIs(t, err, ErrFlowExpired)
	require.Equal(t, Outcome{App: true}, out)
	require.Zero(t, p.exchanges, "no code was exchanged")

	// A state of the web flow that is gone is told apart.
	out, err = flow.Callback(ctx, "made-up", "cookie", "code", "")
	require.ErrorIs(t, err, ErrFlowExpired)
	require.Equal(t, Outcome{}, out)
}

// What ends after the browser proved itself tells the attempt, so the app learns of it.
func TestAppCallbackTellsTheAttemptOfAnEndedSignIn(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()

	_, attempt, _, address, signIn := appStart(t, flow, client, 7)
	state, _ := p.consent(t, address)
	out, err := flow.Callback(ctx, state, signIn, "", "access_denied")
	require.ErrorIs(t, err, ErrFlowDenied)
	require.Equal(t, Outcome{App: true, Attempt: attempt, UserID: 7, Provider: ProviderDrive}, out)

	_, attempt, _, address, signIn = appStart(t, flow, client, 7)
	state, _ = p.consent(t, address)
	out, err = flow.Callback(ctx, state, signIn, "not-the-code", "")
	require.ErrorIs(t, err, ErrFlowProvider)
	require.Equal(t, attempt, out.Attempt)
	require.Empty(t, out.Finish)

	// The account is removed while the provider answers.
	_, attempt, _, address, signIn = appStart(t, flow, client, 7)
	state, code := p.consent(t, address)
	p.tokenAnswer = func(http.ResponseWriter) bool {
		flow.Forget(7)
		return false
	}
	out, err = flow.Callback(ctx, state, signIn, code, "")
	require.ErrorIs(t, err, ErrFlowExpired)
	require.Equal(t, Outcome{App: true, Attempt: attempt, UserID: 7, Provider: ProviderDrive}, out)
	require.Empty(t, flow.granted)
	require.Empty(t, flow.talking)
	require.Empty(t, flow.dropped)
}

// The result of a sign-in from an app is for the account that started and for the app
// that holds the verifier. Every refusal leaves it where it is.
func TestAppResultNeedsAccountAndVerifier(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()
	_, _, _, address, signIn := appStart(t, flow, client, 7)
	state, code := p.consent(t, address)
	out, err := flow.Callback(ctx, state, signIn, code, "")
	require.NoError(t, err)

	type call func(userID int, code, verifier string) error
	calls := map[string]call{
		"peek":   func(u int, c, v string) error { _, err := flow.Peek(u, c, v); return err },
		"finish": func(u int, c, v string) error { _, err := flow.Finish(u, c, v); return err },
		"cancel": flow.Cancel,
	}
	for name, do := range calls {
		require.ErrorIs(t, do(8, out.Finish, appVerifier), ErrFlowAccount, name)
		require.ErrorIs(t, do(7, out.Finish, ""), ErrFlowVerifier, name)
		require.ErrorIs(t, do(7, out.Finish, otherVerifier), ErrFlowVerifier, name)
		require.ErrorIs(t, do(7, out.Finish, appChallenge), ErrFlowVerifier, name+": the challenge is not the verifier")
		require.ErrorIs(t, do(7, "made-up", appVerifier), ErrFlowExpired, name)
		// The account is checked before the verifier.
		require.ErrorIs(t, do(8, out.Finish, otherVerifier), ErrFlowAccount, name)
	}
	require.Len(t, flow.granted, 1)

	// Cancel drops it, once.
	require.NoError(t, flow.Cancel(7, out.Finish, appVerifier))
	require.Empty(t, flow.granted)
	require.ErrorIs(t, flow.Cancel(7, out.Finish, appVerifier), ErrFlowExpired)

	// A result of the web flow is not for apps: with a verifier it is refused and
	// stays, and the web app completes it as ever.
	address, cookie, err := flow.Begin(7, ProviderDrive, client, nil)
	require.NoError(t, err)
	state, code = p.consent(t, address)
	require.False(t, IsAppState(state))
	web, err := flow.Callback(ctx, state, cookie, code, "")
	require.NoError(t, err)
	require.Equal(t, Outcome{Finish: web.Finish, UserID: 7, Provider: ProviderDrive, Account: "user@example.com", AccountKind: AccountAddress}, web)
	for name, do := range calls {
		require.ErrorIs(t, do(7, web.Finish, appVerifier), ErrFlowVerifier, name)
	}
	_, err = flow.Peek(7, web.Finish, "")
	require.ErrorIs(t, err, ErrFlowVerifier)
	require.ErrorIs(t, flow.Cancel(7, web.Finish, ""), ErrFlowVerifier)
	require.Len(t, flow.granted, 1)
	_, err = flow.Finish(7, web.Finish, "")
	require.NoError(t, err)
	_, err = flow.Finish(7, web.Finish, "")
	require.ErrorIs(t, err, ErrFlowExpired)
}

// The five sign-ins an account can have under way include tickets. A further one takes
// the place of the one asked for longest ago; a ticket that became a state keeps its
// place and its age.
func TestAppTicketsShareTheBound(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	flow.now = func() time.Time { return now }
	count := func() int { return len(flow.states) + len(flow.granted) + unused(flow) }

	// The oldest: a ticket that became a state a while after it was made.
	first, _, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	_, firstConfirm, err := flow.Open(first, "")
	require.NoError(t, err)
	now = now.Add(time.Second)
	// A web sign-in, then tickets.
	webAddress, webCookie, err := flow.Begin(7, ProviderDrive, client, nil)
	require.NoError(t, err)
	var tickets []string
	for i := 0; i < maxStates-2; i++ {
		now = now.Add(time.Second)
		ticket, _, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
		require.NoError(t, err)
		tickets = append(tickets, ticket)
	}
	require.Equal(t, maxStates, count())
	// Now the first ticket becomes a state: it runs out later than the web sign-in,
	// and is still the one made first. It takes no further place.
	now = now.Add(time.Second)
	firstAddress, firstSignIn, err := flow.Start(first, firstConfirm, client)
	require.NoError(t, err)
	require.Equal(t, maxStates, count())

	// A sixth pushes out the one made first, which is that state.
	now = now.Add(time.Second)
	_, _, err = flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	require.Equal(t, maxStates, count())
	state, code := p.consent(t, firstAddress)
	_, err = flow.Callback(ctx, state, firstSignIn, code, "")
	require.ErrorIs(t, err, ErrFlowExpired)
	// A seventh pushes out the web sign-in, which is now the oldest: one from an app
	// can take the place of one from the web app.
	now = now.Add(time.Second)
	_, _, err = flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	state, code = p.consent(t, webAddress)
	_, err = flow.Callback(ctx, state, webCookie, code, "")
	require.ErrorIs(t, err, ErrFlowExpired)
	// The next oldest is the first of the other tickets.
	now = now.Add(time.Second)
	_, _, err = flow.Begin(7, ProviderDrive, client, nil)
	require.NoError(t, err)
	_, _, err = flow.Open(tickets[0], "")
	require.ErrorIs(t, err, ErrFlowExpired)
	_, _, err = flow.Open(tickets[1], "")
	require.NoError(t, err)
	require.Equal(t, maxStates, count())

	// Another account has its own five.
	_, _, err = flow.Ticket(8, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	require.Equal(t, maxStates+1, count())
}

func unused(f *Flow) int {
	n := 0
	for _, t := range f.tickets {
		if t.state == "" {
			n++
		}
	}
	return n
}

// The result of a callback takes the place of its callback: the sign-in is never counted
// twice, and a result is the last to give way.
func TestAppResultKeepsThePlaceOfItsSignIn(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	flow.now = func() time.Time { return now }

	// The oldest sign-in ends as a result.
	_, _, _, address, signIn := appStart(t, flow, client, 7)
	state, code := p.consent(t, address)
	out, err := flow.Callback(ctx, state, signIn, code, "")
	require.NoError(t, err)
	require.Empty(t, flow.talking)
	for i := 0; i < maxStates-1; i++ {
		now = now.Add(time.Second)
		_, _, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
		require.NoError(t, err)
	}
	// One more: a ticket gives way, not the result, though the result is older.
	now = now.Add(time.Second)
	_, _, err = flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	require.Equal(t, maxStates-1, unused(flow))
	_, err = flow.Peek(7, out.Finish, appVerifier)
	require.NoError(t, err)
}

// Tickets and receipts run out by themselves and go with the account.
func TestAppTicketsAndReceiptsAreSwept(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	flow.now = func() time.Time { return now }

	_, _, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	now = now.Add(ticketLife + time.Second)
	flow.mu.Lock()
	flow.sweep()
	flow.mu.Unlock()
	require.Empty(t, flow.tickets)

	_, _, _, address, signIn := appStart(t, flow, client, 7)
	state, code := p.consent(t, address)
	out, err := flow.Callback(ctx, state, signIn, code, "")
	require.NoError(t, err)
	g, err := flow.Finish(7, out.Finish, appVerifier)
	require.NoError(t, err)
	flow.Remember(out.Finish, g, "storage-id")
	require.False(t, flow.Busy(ProviderDrive), "a receipt is nothing at the provider")

	// The receipt is for the account and the verifier of the result.
	id, ok := flow.Receipt(7, out.Finish, appVerifier)
	require.True(t, ok)
	require.Equal(t, "storage-id", id)
	for _, bad := range []struct {
		userID         int
		code, verifier string
	}{{8, out.Finish, appVerifier}, {7, out.Finish, otherVerifier}, {7, out.Finish, ""}, {7, "made-up", appVerifier}} {
		_, ok := flow.Receipt(bad.userID, bad.code, bad.verifier)
		require.False(t, ok)
	}
	now = now.Add(receiptLife + time.Second)
	_, ok = flow.Receipt(7, out.Finish, appVerifier)
	require.False(t, ok)
	require.Empty(t, flow.receipts)

	// A result of the web flow leaves no receipt.
	flow.Remember("web", &Granted{UserID: 7}, "storage-id")
	require.Empty(t, flow.receipts)

	// Forget takes tickets and receipts along.
	_, _, err = flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	_, _, err = flow.Ticket(8, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	flow.Remember(out.Finish, g, "storage-id")
	flow.Forget(7)
	require.Len(t, flow.tickets, 1)
	require.Empty(t, flow.receipts)
}

// While access is being taken back at a provider no state is made there, as for the web
// flow. The ticket stays, so the same press works a moment later.
func TestAppStartWaitsForARevoke(t *testing.T) {
	flow, _, client := testFlow(t)
	ticket, _, err := flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	_, confirm, err := flow.Open(ticket, "")
	require.NoError(t, err)

	done, ok := flow.Revoking(ProviderDrive)
	require.True(t, ok, "a ticket does not hold a revoke up")
	_, _, err = flow.Start(ticket, confirm, client)
	require.ErrorIs(t, err, ErrFlowWait)
	done()
	_, _, err = flow.Start(ticket, confirm, client)
	require.NoError(t, err)
}

// Microsoft is asked for its account chooser when an app starts, and only then.
func TestAppConsentAsksForTheAccountChooser(t *testing.T) {
	flow := NewFlow(Specs(), http.DefaultClient)
	client := Client{ID: "client-id", Secret: "client-secret", Redirect: testRedirect}
	query := func(address string) url.Values {
		u, err := url.Parse(address)
		require.NoError(t, err)
		return u.Query()
	}

	web, _, err := flow.Begin(7, ProviderOneDrive, client, nil)
	require.NoError(t, err)
	require.Empty(t, query(web).Get("prompt"))

	ticket, _, err := flow.Ticket(7, ProviderOneDrive, nil, appChallenge)
	require.NoError(t, err)
	_, confirm, err := flow.Open(ticket, "")
	require.NoError(t, err)
	app, _, err := flow.Start(ticket, confirm, client)
	require.NoError(t, err)
	require.Equal(t, "select_account", query(app).Get("prompt"))
	require.Equal(t, query(web).Get("scope"), query(app).Get("scope"))

	// Google is asked for consent either way.
	ticket, _, err = flow.Ticket(7, ProviderDrive, nil, appChallenge)
	require.NoError(t, err)
	_, confirm, err = flow.Open(ticket, "")
	require.NoError(t, err)
	app, _, err = flow.Start(ticket, confirm, client)
	require.NoError(t, err)
	require.Equal(t, "consent", query(app).Get("prompt"))
}

// What an app is shown of an account: the name that says most, and what kind it is.
func TestAppShownAccountNames(t *testing.T) {
	name, kind := shown([2]string{"user@example.com", AccountAddress}, [2]string{"Alex", AccountName})
	require.Equal(t, []string{"user@example.com", AccountAddress}, []string{name, kind})
	name, kind = shown([2]string{"", AccountSignInName}, [2]string{"Alex", AccountName})
	require.Equal(t, []string{"Alex", AccountName}, []string{name, kind})
	name, kind = shown([2]string{"", AccountAddress}, [2]string{"", AccountName})
	require.Equal(t, []string{"", AccountNone}, []string{name, kind})
}

// Microsoft's "mail" is a field the administrator of an organisation fills at will. It
// stays the label of a stored connection, as it was, and is never what an app shows as
// the account before it stores.
func TestAppShownAccountAtMicrosoft(t *testing.T) {
	answer := func(me map[string]string) APICall {
		return func(_ context.Context, address string, out any) error {
			data := map[string]string{"id": "drive-1", "driveType": "business"}
			if !strings.Contains(address, "/me/drive") {
				data = me
			}
			raw, _ := json.Marshal(data)
			return json.Unmarshal(raw, out)
		}
	}
	for _, c := range []struct {
		me                 map[string]string
		label, shown, kind string
	}{
		{map[string]string{"id": "u", "mail": "boss@example.org", "userPrincipalName": "alex@example.onmicrosoft.com", "displayName": "Alex"},
			"boss@example.org", "alex@example.onmicrosoft.com", AccountSignInName},
		{map[string]string{"id": "u", "mail": "boss@example.org", "displayName": "Alex"}, "boss@example.org", "Alex", AccountName},
		{map[string]string{"id": "u", "mail": "boss@example.org"}, "boss@example.org", "", AccountNone},
	} {
		identity, err := identifyOneDrive(context.Background(), answer(c.me), "https://graph.example")
		require.NoError(t, err)
		require.Equal(t, []string{c.label, c.shown, c.kind}, []string{identity.Label, identity.Shown, identity.Kind})
	}
}

// ---- the Manager

// appSignIn plays app, browser and provider up to the finish code.
func (f *managerFixture) appSignIn(t *testing.T, userID int, reconnect string) (code string) {
	t.Helper()
	ctx := context.Background()
	ticket, _, err := f.AuthorizeApp(ctx, userID, ProviderDrive, reconnect, appChallenge)
	require.NoError(t, err)
	_, confirm, err := f.OpenTicket(ticket, "")
	require.NoError(t, err)
	address, signIn, _, err := f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
	require.NoError(t, err)
	state, grant := f.provider.consent(t, address)
	out, err := f.Callback(ctx, state, signIn, grant, "")
	require.NoError(t, err)
	return out.Finish
}

func anyAccount(context.Context, int) error { return nil }

func (f *managerFixture) setUp(t *testing.T) {
	t.Helper()
	secret := "client-secret"
	require.NoError(t, f.SetProvider(context.Background(), ProviderDrive, "client-id", &secret))
}

func TestManagerAppSignIn(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()

	// The same checks as for the web flow, before anything is kept.
	_, _, err := f.AuthorizeApp(ctx, 7, ProviderDrive, "", appChallenge)
	require.Equal(t, ReasonNotConfigured, ReasonOf(err))
	f.setUp(t)
	_, _, err = f.AuthorizeApp(ctx, 7, ProviderDrive, "", "not-a-challenge")
	require.Equal(t, serializer.CodeParamErr, appCodeOf(err))
	_, _, err = f.AuthorizeApp(ctx, 7, "dropbox", "", appChallenge)
	require.Equal(t, serializer.CodeParamErr, appCodeOf(err))
	_, _, err = f.AuthorizeApp(ctx, 7, ProviderDrive, "aaaaaaaaaaaaaaaa", appChallenge)
	require.ErrorIs(t, err, ErrNoStorage)
	f.gone = map[int]bool{9: true}
	_, _, err = f.AuthorizeApp(ctx, 9, ProviderDrive, "", appChallenge)
	require.Equal(t, ReasonExpired, ReasonOf(err))
	require.Empty(t, f.flow.tickets)

	code := f.appSignIn(t, 7, "")

	// The look before storing: who signed in, and nothing is stored or used up.
	for i := 0; i < 2; i++ {
		seen, err := f.Preview(7, code, appVerifier)
		require.NoError(t, err)
		require.Equal(t, &SignedIn{Provider: ProviderDrive, Account: "user@example.com", AccountKind: AccountAddress}, seen)
	}
	list, err := f.List(ctx, 7)
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = f.Preview(8, code, appVerifier)
	require.Equal(t, "account", ReasonOf(err))
	_, err = f.Preview(7, code, otherVerifier)
	require.Equal(t, "verifier", ReasonOf(err))
	_, err = f.Finish(ctx, 7, code, "")
	require.Equal(t, "verifier", ReasonOf(err))
	_, err = f.Finish(ctx, 7, code, otherVerifier)
	require.Equal(t, "verifier", ReasonOf(err))

	storage, err := f.Finish(ctx, 7, code, appVerifier)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", storage.Account)

	// The answer was lost: the app asks again and gets the same storage, read again.
	_, err = f.Rename(ctx, 7, storage.ID, "Photos")
	require.NoError(t, err)
	again, err := f.Finish(ctx, 7, code, appVerifier)
	require.NoError(t, err)
	require.Equal(t, storage.ID, again.ID)
	require.Equal(t, "Photos", again.Name)
	list, err = f.List(ctx, 7)
	require.NoError(t, err)
	require.Len(t, list, 1)
	// The repeat is for that app and that account only, and only for storing.
	_, err = f.Finish(ctx, 7, code, otherVerifier)
	require.Equal(t, ReasonExpired, ReasonOf(err))
	_, err = f.Finish(ctx, 7, code, "")
	require.Equal(t, ReasonExpired, ReasonOf(err))
	_, err = f.Finish(ctx, 8, code, appVerifier)
	require.Equal(t, ReasonExpired, ReasonOf(err))
	_, err = f.Preview(7, code, appVerifier)
	require.Equal(t, ReasonExpired, ReasonOf(err))
	require.Equal(t, ReasonExpired, ReasonOf(f.Cancel(7, code, appVerifier)))
	// Disconnected meanwhile: the repeat says the storage is gone.
	require.NoError(t, f.Disconnect(ctx, 7, storage.ID))
	_, err = f.Finish(ctx, 7, code, appVerifier)
	require.ErrorIs(t, err, ErrNoStorage)
}

func appCodeOf(err error) int {
	var app serializer.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	return -1
}

// Cancel drops the result; nothing is stored afterwards.
func TestManagerAppCancel(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	f.setUp(t)
	code := f.appSignIn(t, 7, "")

	require.Equal(t, "account", ReasonOf(f.Cancel(8, code, appVerifier)))
	require.Equal(t, "verifier", ReasonOf(f.Cancel(7, code, otherVerifier)))
	require.NoError(t, f.Cancel(7, code, appVerifier))
	_, err := f.Finish(ctx, 7, code, appVerifier)
	require.Equal(t, ReasonExpired, ReasonOf(err))
	list, err := f.List(ctx, 7)
	require.NoError(t, err)
	require.Empty(t, list)
}

// Two finishes of the same code at the same time store once, and both get the storage.
func TestManagerAppFinishTwiceAtOnce(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	f.setUp(t)
	code := f.appSignIn(t, 7, "")

	var (
		wg   sync.WaitGroup
		ids  [4]string
		errs [4]error
	)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			storage, err := f.Finish(ctx, 7, code, appVerifier)
			if errs[i] = err; err == nil {
				ids[i] = storage.ID
			}
		}()
	}
	wg.Wait()
	for i := range ids {
		require.NoError(t, errs[i])
		require.Equal(t, ids[0], ids[i])
	}
	list, err := f.List(ctx, 7)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

// What was true when the app asked is checked again when the button is pressed.
func TestManagerAppStartChecksAgain(t *testing.T) {
	ctx := context.Background()
	open := func(t *testing.T, f *managerFixture, reconnect string) (ticket, confirm, attempt string) {
		t.Helper()
		ticket, attempt, err := f.AuthorizeApp(ctx, 7, ProviderDrive, reconnect, appChallenge)
		require.NoError(t, err)
		_, confirm, err = f.OpenTicket(ticket, "")
		require.NoError(t, err)
		return ticket, confirm, attempt
	}
	// refused: the ticket is used up, also for a second press.
	refused := func(t *testing.T, f *managerFixture, ticket, confirm, attempt, reason string, allowed func(context.Context, int) error) {
		t.Helper()
		_, _, view, err := f.StartTicket(ctx, ticket, confirm, testRedirect, allowed)
		var r *TicketRefused
		require.ErrorAs(t, err, &r)
		require.Equal(t, reason, r.Reason)
		require.Equal(t, attempt, view.Attempt)
		_, _, view, err = f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
		require.ErrorIs(t, err, ErrFlowExpired)
		require.Nil(t, view)
		require.Empty(t, f.flow.states)
	}

	t.Run("not this browser", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, _ := open(t, f, "")
		called := false
		_, _, view, err := f.StartTicket(ctx, ticket, "made-up", testRedirect, func(context.Context, int) error {
			called = true
			return nil
		})
		require.ErrorIs(t, err, ErrFlowExpired)
		require.Nil(t, view)
		require.False(t, called, "nothing is looked up for a browser that does not own the ticket")
		// The ticket is still there for its owner.
		_, _, _, err = f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
		require.NoError(t, err)
	})

	t.Run("may no longer connect", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, attempt := open(t, f, "")
		refused(t, f, ticket, confirm, attempt, ReasonNotAllowed, func(context.Context, int) error { return ErrNotAllowed })
	})

	t.Run("account blocked or gone", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, attempt := open(t, f, "")
		refused(t, f, ticket, confirm, attempt, ReasonExpired, func(context.Context, int) error { return ErrFlowExpired })

		// The caller read the account a moment ago and says it is there: it is not
		// asked for a second time, so trouble at that second look cannot end the ticket.
		ticket, confirm, _ = open(t, f, "")
		f.gone = map[int]bool{7: true}
		_, _, _, err := f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
		require.NoError(t, err)
	})

	t.Run("two presses at once decide the same", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, _ := open(t, f, "")
		// The first press is in its checks while the second arrives; by then the account
		// may no longer start. The second finds the sign-in of the first and repeats it.
		inside, release := make(chan struct{}), make(chan struct{})
		type answer struct {
			address string
			err     error
		}
		first, second := make(chan answer, 1), make(chan answer, 1)
		go func() {
			address, _, _, err := f.StartTicket(ctx, ticket, confirm, testRedirect, func(context.Context, int) error {
				close(inside)
				<-release
				return nil
			})
			first <- answer{address, err}
		}()
		<-inside
		go func() {
			address, _, _, err := f.StartTicket(ctx, ticket, confirm, testRedirect, func(context.Context, int) error { return ErrNotAllowed })
			second <- answer{address, err}
		}()
		time.Sleep(50 * time.Millisecond)
		close(release)
		a, b := <-first, <-second
		require.NoError(t, a.err)
		require.NoError(t, b.err)
		require.Equal(t, a.address, b.address)
		require.Len(t, f.flow.states, 1)
	})

	t.Run("account being removed", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, attempt := open(t, f, "")
		done := f.Leaving(7)
		refused(t, f, ticket, confirm, attempt, ReasonExpired, anyAccount)
		done()

		// Once the account is removed its tickets are gone with it.
		ticket, confirm, _ = open(t, f, "")
		require.NoError(t, f.DisconnectUser(ctx, 7))
		_, _, view, err := f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
		require.ErrorIs(t, err, ErrFlowExpired)
		require.Nil(t, view)
	})

	t.Run("provider no longer set up", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, attempt := open(t, f, "")
		require.NoError(t, f.SetProvider(ctx, ProviderDrive, "", nil))
		refused(t, f, ticket, confirm, attempt, ReasonSetup, anyAccount)
	})

	t.Run("the client is read again", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, _ := open(t, f, "")
		secret := "another-secret"
		require.NoError(t, f.SetProvider(ctx, ProviderDrive, "another-client", &secret))
		address, _, _, err := f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
		require.NoError(t, err)
		u, err := url.Parse(address)
		require.NoError(t, err)
		require.Equal(t, "another-client", u.Query().Get("client_id"))
		require.Equal(t, testRedirect, u.Query().Get("redirect_uri"))
	})

	t.Run("as many storages as there can be", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, attempt := open(t, f, "")
		for i := 0; i < MaxPerUser; i++ {
			_, err := f.Create(ctx, 7, ProviderDrive, &Identity{Key: "k" + string(rune('a'+i)), Label: "x"}, `{"refresh_token":"r"}`)
			require.NoError(t, err)
		}
		refused(t, f, ticket, confirm, attempt, ReasonTooMany, anyAccount)
	})

	t.Run("trouble that may pass keeps the ticket", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, attempt := open(t, f, "")
		broken := serializer.NewError(serializer.CodeDBError, "Failed to load the account", errors.New("database is locked"))
		_, _, view, err := f.StartTicket(ctx, ticket, confirm, testRedirect, func(context.Context, int) error { return broken })
		require.ErrorIs(t, err, ErrFlowWait)
		require.Equal(t, attempt, view.Attempt)
		// Taking access back at the provider: the same.
		done, ok := f.flow.Revoking(ProviderDrive)
		require.True(t, ok)
		_, _, view, err = f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
		require.ErrorIs(t, err, ErrFlowWait)
		require.Equal(t, attempt, view.Attempt)
		done()
		_, _, _, err = f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
		require.NoError(t, err)
	})

	t.Run("the same press again", func(t *testing.T) {
		f := newManagerFixture(t)
		f.setUp(t)
		ticket, confirm, _ := open(t, f, "")
		address, signIn, _, err := f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
		require.NoError(t, err)
		// Nothing is checked again and nothing new is made, even if the account may
		// no longer start: the sign-in that is under way goes on as it would anyway.
		again, signInAgain, view, err := f.StartTicket(ctx, ticket, confirm, testRedirect, func(context.Context, int) error { return ErrNotAllowed })
		require.NoError(t, err)
		require.True(t, view.Used)
		require.Equal(t, []string{address, signIn}, []string{again, signInAgain})
		require.Len(t, f.flow.states, 1)
	})
}

// Connecting again from an app: the same account at the provider renews the token, and a
// sign-in that was overtaken by another one stores nothing.
func TestManagerAppReconnect(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	f.setUp(t)
	storage, err := f.connect(t, 7, "")
	require.NoError(t, err)

	code := f.appSignIn(t, 7, storage.ID)
	seen, err := f.Preview(7, code, appVerifier)
	require.NoError(t, err)
	require.Equal(t, storage.ID, seen.Reconnect)
	renewed, err := f.Finish(ctx, 7, code, appVerifier)
	require.NoError(t, err)
	require.Equal(t, storage.ID, renewed.ID)
	require.Equal(t, []string{storage.ID}, f.closed)

	// Overtaken before the button: the ticket was made for the generation before.
	ticket, attempt, err := f.AuthorizeApp(ctx, 7, ProviderDrive, storage.ID, appChallenge)
	require.NoError(t, err)
	_, confirm, err := f.OpenTicket(ticket, "")
	require.NoError(t, err)
	_, err = f.connect(t, 7, storage.ID)
	require.NoError(t, err)
	_, _, view, err := f.StartTicket(ctx, ticket, confirm, testRedirect, anyAccount)
	var refused *TicketRefused
	require.ErrorAs(t, err, &refused)
	require.Equal(t, ReasonExpired, refused.Reason)
	require.Equal(t, attempt, view.Attempt)

	// Overtaken between the provider and the finish: the app is told so in its own
	// word, and the storage keeps what the other sign-in stored.
	code = f.appSignIn(t, 7, storage.ID)
	_, err = f.connect(t, 7, storage.ID)
	require.NoError(t, err)
	_, err = f.Finish(ctx, 7, code, appVerifier)
	require.Equal(t, "overtaken", ReasonOf(err))
	require.Equal(t, serializer.CodeParamErr, appCodeOf(err))
	// Used up: no repeat.
	_, err = f.Finish(ctx, 7, code, appVerifier)
	require.Equal(t, ReasonExpired, ReasonOf(err))

	// The web app goes on getting the word it knows.
	address, cookie, err := f.Authorize(ctx, 7, ProviderDrive, storage.ID, testRedirect)
	require.NoError(t, err)
	state, grant := f.provider.consent(t, address)
	web, err := f.Callback(ctx, state, cookie, grant, "")
	require.NoError(t, err)
	_, err = f.connect(t, 7, storage.ID)
	require.NoError(t, err)
	_, err = f.Finish(ctx, 7, web.Finish, "")
	require.Equal(t, ReasonExpired, ReasonOf(err))

	// Another account at the provider is refused, as in the web flow.
	code = f.appSignIn(t, 7, storage.ID)
	f.provider.account = "account-2"
	code = f.appSignIn(t, 7, storage.ID)
	_, err = f.Finish(ctx, 7, code, appVerifier)
	require.Equal(t, ReasonOtherAccount, ReasonOf(err))

	// The storage is gone when the finish comes.
	f.provider.account = "account-1"
	code = f.appSignIn(t, 7, storage.ID)
	require.NoError(t, f.Disconnect(ctx, 7, storage.ID))
	_, err = f.Finish(ctx, 7, code, appVerifier)
	require.ErrorIs(t, err, ErrNoStorage)
}

// An account that signed in at the provider already cannot be connected twice, from an
// app as little as from the web app.
func TestManagerAppFinishRefusals(t *testing.T) {
	f := newManagerFixture(t)
	ctx := context.Background()
	f.setUp(t)
	_, err := f.connect(t, 7, "")
	require.NoError(t, err)

	code := f.appSignIn(t, 7, "")
	_, err = f.Finish(ctx, 7, code, appVerifier)
	require.Equal(t, ReasonExists, ReasonOf(err))
	// Used up, and nothing to repeat.
	_, err = f.Finish(ctx, 7, code, appVerifier)
	require.Equal(t, ReasonExpired, ReasonOf(err))

	// An account that is being removed gets nothing stored and nothing shown.
	code = f.appSignIn(t, 8, "")
	done := f.Leaving(8)
	_, err = f.Finish(ctx, 8, code, appVerifier)
	require.Equal(t, ReasonExpired, ReasonOf(err))
	done()
}

// The kinds of account name an app is shown before it stores.
func TestManagerAppPreviewKinds(t *testing.T) {
	f := newManagerFixture(t)
	f.setUp(t)
	for _, c := range []struct {
		identity *Identity
		want     SignedIn
	}{
		{&Identity{Key: "a", Shown: "user@example.com", Kind: AccountAddress}, SignedIn{Account: "user@example.com", AccountKind: AccountAddress}},
		{&Identity{Key: "b", Shown: "alex@example.onmicrosoft.com", Kind: AccountSignInName}, SignedIn{Account: "alex@example.onmicrosoft.com", AccountKind: AccountSignInName}},
		{&Identity{Key: "c", Shown: "Alex", Kind: AccountName}, SignedIn{Account: "Alex", AccountKind: AccountName}},
		{&Identity{Key: "d", Kind: AccountNone}, SignedIn{AccountKind: AccountNone}},
		// A provider of an older kind that tells neither.
		{&Identity{Key: "e", Label: "Somebody"}, SignedIn{AccountKind: AccountNone}},
	} {
		f.specs[ProviderDrive].Identify = func(context.Context, APICall) (*Identity, error) { return c.identity, nil }
		code := f.appSignIn(t, 7, "")
		seen, err := f.Preview(7, code, appVerifier)
		require.NoError(t, err)
		c.want.Provider = ProviderDrive
		require.Equal(t, &c.want, seen)
		require.NoError(t, f.Cancel(7, code, appVerifier))
	}
}
