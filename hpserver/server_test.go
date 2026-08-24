package hpserver

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	blaze "github.com/local/reorigin-hotpursuit"
	"github.com/local/reorigin-hotpursuit/legacyfire"
)

func TestCapturedBootstrapFixtures(t *testing.T) {
	s := New(nil)
	tests := []struct {
		component, command, id uint16
		tdf                    map[string]interface{}
		payload                int
	}{
		{5, 1, 0, nil, 74},
		{9, 7, 0, nil, 191},
		{9, 2, 1, nil, 9},
		{9, 1, 2, map[string]interface{}{"CFID": "Url"}, 652},
		{9, 1, 3, map[string]interface{}{"CFID": "Game"}, 102},
	}
	for _, tc := range tests {
		req := &legacyfire.Frame{Header: legacyfire.Header{Component: tc.component, Command: tc.command, MessageID: tc.id}, TDF: tc.tdf}
		got, err := s.Handlers[key(tc.component, tc.command)](context.Background(), &Session{}, req)
		if err != nil {
			t.Fatal(err)
		}
		if got.Header.MessageType != legacyfire.MessageReply || got.Header.MessageID != tc.id || len(got.Payload) != tc.payload {
			t.Fatalf("%d/%d: header=%+v payload=%d", tc.component, tc.command, got.Header, len(got.Payload))
		}
		var wire bytes.Buffer
		if err := legacyfire.Write(&wire, got); err != nil {
			t.Fatal(err)
		}
		decoded, err := legacyfire.Read(&wire)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Payload) != 0 && decoded.TDF == nil {
			t.Fatalf("%d/%d: fixture is not valid TDF", tc.component, tc.command)
		}
	}
}

func TestCapturedUtilityConfigRequestsSelectDistinctFixtures(t *testing.T) {
	s := New(nil)
	tests := []struct {
		raw         string
		wantValue   string
		wantPayload int
	}{
		{"0009000900010000000000028e6a64010455726c00", "Url", 652},
		{"000a000900010000000000038e6a64010547616d6500", "Game", 102},
	}
	for _, tc := range tests {
		req, err := legacyfire.Read(bytes.NewReader(mustHex(tc.raw)))
		if err != nil {
			t.Fatal(err)
		}
		if req.TDF["CFID"] != tc.wantValue {
			t.Fatalf("decoded CFID=%v; TDF=%v", req.TDF["CFID"], req.TDF)
		}
		got, err := s.Handlers[key(9, 1)](context.Background(), &Session{}, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Payload) != tc.wantPayload {
			t.Fatalf("CFID=%s payload=%d", tc.wantValue, len(got.Payload))
		}
	}
}

func TestUtilityServerTimeIsCurrent(t *testing.T) {
	s := New(nil)
	req := &legacyfire.Frame{Header: legacyfire.Header{Component: 9, Command: 2, MessageID: 1}}
	got, err := s.Handlers[key(9, 2)](context.Background(), &Session{}, req)
	if err != nil {
		t.Fatal(err)
	}
	var wire bytes.Buffer
	if err := legacyfire.Write(&wire, got); err != nil {
		t.Fatal(err)
	}
	decoded, err := legacyfire.Read(&wire)
	if err != nil {
		t.Fatal(err)
	}
	serverTime, ok := decoded.TDF["STIM"].(int64)
	if !ok || serverTime < time.Now().Unix()-2 || serverTime > time.Now().Unix()+2 {
		t.Fatalf("STIM=%v", decoded.TDF["STIM"])
	}
}

func TestSilentLoginReturnsSyntheticFullLoginResponse(t *testing.T) {
	s := New(nil)
	session := &Session{}
	req := &legacyfire.Frame{Header: legacyfire.Header{
		Component: ComponentAuthentication,
		Command:   CommandSilentLogin,
		MessageID: 4,
	}}

	got, err := s.Handlers[key(ComponentAuthentication, CommandSilentLogin)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 4 {
		t.Fatalf("header=%+v", got.Header)
	}

	var wire bytes.Buffer
	if err := legacyfire.Write(&wire, got); err != nil {
		t.Fatal(err)
	}
	decoded, err := legacyfire.Read(&wire)
	if err != nil {
		t.Fatal(err)
	}
	sess, ok := decoded.TDF["SESS"].(map[string]interface{})
	if !ok {
		t.Fatalf("SESS=%#v", decoded.TDF["SESS"])
	}
	persona, ok := sess["PDTL"].(map[string]interface{})
	if !ok || sess["UID"] != int64(1) || sess["BUID"] != int64(1) || persona["PID"] != int64(1) || persona["DSNM"] != "Player" {
		t.Fatalf("SESS=%#v", sess)
	}
	if session.AccountID != 1 || session.PersonaID != 1 || session.SessionID != 1 || session.Persona != "Player" {
		t.Fatalf("session=%+v", session)
	}
	if len(session.Notifications) != 2 {
		t.Fatalf("notifications=%d", len(session.Notifications))
	}
	added := session.Notifications[0]
	if added.Header.Component != ComponentUserSessions || added.Header.Command != NotifyUserAdded || added.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("UserAdded header=%+v", added.Header)
	}
	addedTDF, consumed := blaze.DecodeTDF(added.Payload)
	if consumed < 0 {
		t.Fatalf("invalid UserAdded payload: %x", added.Payload)
	}
	user, ok := addedTDF["USER"].(map[string]interface{})
	if !ok || user["ID"] != int64(1) || user["AID"] != int64(1) || user["NAME"] != "Player" {
		t.Fatalf("UserAdded USER=%#v", addedTDF["USER"])
	}
	data, ok := addedTDF["DATA"].(map[string]interface{})
	if !ok || data["CTY"] != "US" || data["HWFG"] != int64(0) {
		t.Fatalf("UserAdded DATA=%#v", addedTDF["DATA"])
	}
	extended := session.Notifications[1]
	if extended.Header.Component != ComponentUserSessions || extended.Header.Command != NotifyExtendedData || extended.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("ExtendedData header=%+v", extended.Header)
	}
	extendedTDF, consumed := blaze.DecodeTDF(extended.Payload)
	if consumed < 0 || extendedTDF["USID"] != int64(1) {
		t.Fatalf("ExtendedData TDF=%#v", extendedTDF)
	}
	if _, wrongTag := extendedTDF["USER"]; wrongTag {
		t.Fatalf("ExtendedData contains incorrect USER tag: %#v", extendedTDF)
	}
}

func TestRedirectorConnectionDoesNotConsumeAuthenticatedIdentity(t *testing.T) {
	s := New(nil)
	redirector := s.newSession(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50000})
	if redirector.PersonaID != 0 {
		t.Fatalf("unauthenticated redirector persona=%d", redirector.PersonaID)
	}
	s.removeSession(redirector)
	blazeSession := s.newSession(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50001})
	s.authenticateSession(blazeSession)
	if blazeSession.AccountID != 1 || blazeSession.PersonaID != 1 || blazeSession.SessionID != 1 || blazeSession.Persona != "Player" {
		t.Fatalf("first authenticated session=%+v", blazeSession)
	}
	second := s.newSession(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50002})
	s.authenticateSession(second)
	if second.AccountID != 2 || second.PersonaID != 2 || second.SessionID != 2 || second.Persona != "Player2" {
		t.Fatalf("second authenticated session=%+v", second)
	}
}

func TestSilentLoginAllocatesCanonicalIdentityOnActiveCollision(t *testing.T) {
	s := New(nil)
	// Captured failures prove that mixing identities across FullLogin/UserAdded
	// stalls after PostAuth. A duplicate request may be provisioned as a new user,
	// but every field exposed to that connection must use the newly assigned tuple.
	var authenticated []*Session
	for i, wantID := range []int64{2, 3} {
		req := &legacyfire.Frame{Header: legacyfire.Header{Component: ComponentAuthentication, Command: CommandSilentLogin, MessageID: 4}, TDF: map[string]interface{}{"PID": int64(2)}}
		session := s.newSession(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50100 + i})
		reply, err := s.Handlers[key(ComponentAuthentication, CommandSilentLogin)](context.Background(), session, req)
		if err != nil {
			t.Fatal(err)
		}
		if session.PersonaID != wantID || session.AccountID != wantID || session.Persona != personaName(wantID) {
			t.Fatalf("login %d session=%+v", i, session)
		}
		decoded, consumed := blaze.DecodeTDF(reply.Payload)
		if consumed != len(reply.Payload) {
			t.Fatalf("login %d invalid payload", i)
		}
		loginSession := decoded["SESS"].(map[string]interface{})
		persona := loginSession["PDTL"].(map[string]interface{})
		if persona["DSNM"] != personaName(wantID) || persona["PID"] != wantID {
			t.Fatalf("login %d persona=%#v", i, persona)
		}
		if loginSession["BUID"] != wantID || loginSession["UID"] != wantID {
			t.Fatalf("login %d account identity=%#v", i, loginSession)
		}
		selfAdded, _ := blaze.DecodeTDF(session.Notifications[0].Payload)
		selfUser := selfAdded["USER"].(map[string]interface{})
		setup := gameSetup(newGame(int64(i), session, []*Session{session}, map[interface{}]interface{}{}, 8, 132, 1))
		selfPlayer := setup["PROS"].(blaze.TypedList).Items[0].(map[string]interface{})
		if selfUser["AID"] != session.AccountID || selfUser["ID"] != selfPlayer["PID"] ||
			selfUser["NAME"] != selfPlayer["NAME"] || selfPlayer["UID"] != session.SessionID {
			t.Fatalf("login %d auth/roster identity mismatch USER=%#v PROS=%#v", i, selfUser, selfPlayer)
		}
		authenticated = append(authenticated, session)
	}

	reconnectRequest := &legacyfire.Frame{Header: legacyfire.Header{Component: ComponentAuthentication, Command: CommandSilentLogin, MessageID: 4}, TDF: map[string]interface{}{"PID": int64(2)}}
	oldSessionID := authenticated[0].SessionID
	s.removeSession(authenticated[0])
	reconnected := s.newSession(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50104})
	reply, err := s.Handlers[key(ComponentAuthentication, CommandSilentLogin)](context.Background(), reconnected, reconnectRequest)
	if err != nil || reply.Header.Error != 0 || reconnected.PersonaID != 2 || reconnected.Persona != "Player2" || reconnected.SessionID == oldSessionID {
		t.Fatalf("reconnected identity=%+v reply=%+v err=%v", reconnected, reply.Header, err)
	}
}

func TestPostAuthReturnsSyntheticConfiguration(t *testing.T) {
	s := New(nil)
	req := &legacyfire.Frame{Header: legacyfire.Header{
		Component: ComponentUtility,
		Command:   CommandPostAuth,
		MessageID: 5,
	}}
	got, err := s.Handlers[key(ComponentUtility, CommandPostAuth)](
		context.Background(), &Session{}, req)
	if err != nil {
		t.Fatal(err)
	}

	var wire bytes.Buffer
	if err := legacyfire.Write(&wire, got); err != nil {
		t.Fatal(err)
	}
	decoded, err := legacyfire.Read(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Header.MessageType != legacyfire.MessageReply || decoded.Header.Error != 0 || decoded.Header.MessageID != 5 {
		t.Fatalf("header=%+v", decoded.Header)
	}
	for _, tag := range []string{"PSS", "TELE", "TICK", "UROP"} {
		if _, ok := decoded.TDF[tag].(map[string]interface{}); !ok {
			t.Fatalf("%s=%#v; TDF=%#v", tag, decoded.TDF[tag], decoded.TDF)
		}
	}
}

func TestUserSettingsLoadReturnsShareUsagePreference(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"00160009000a00000000000d" +
			"ae5e40010c53484152455f555341474500d699000000")))
	if err != nil {
		t.Fatal(err)
	}
	if req.TDF["KEY"] != "SHARE_USAGE" || req.TDF["UID"] != int64(0) {
		t.Fatalf("request TDF=%#v", req.TDF)
	}
	got, err := s.Handlers[key(ComponentUtility, CommandUserSettingsLoad)](
		context.Background(), &Session{AccountID: 1}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 13 || len(got.Payload) != 27 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}

	var wire bytes.Buffer
	if err := legacyfire.Write(&wire, got); err != nil {
		t.Fatal(err)
	}
	decoded, err := legacyfire.Read(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.TDF["KEY"] != "SHARE_USAGE" || decoded.TDF["DATA"] != "TRUE" {
		t.Fatalf("response TDF=%#v", decoded.TDF)
	}
}

func TestSetClientMetricsAcknowledgesCapturedRequest(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"002300090016000000000006" +
			"d6497601196565726f20696e632e206565726f2050726f20302e302e3100d73d210001")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Handlers[key(ComponentUtility, CommandSetMetrics)](
		context.Background(), &Session{}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 6 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
}

func TestUpdateNetworkInfoAcknowledgesCapturedRequest(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"004178020014000000000006" +
			"8649320602da1b3503978a7003a700000000c2fcb4000000a6ea7003a70000008793c08a18c2fcb4008b390000bb1bf303922c330000ba1d340004d62c33000000")))
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{}
	got, err := s.Handlers[key(ComponentUserSessions, CommandUpdateNetworkInfo)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 6 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	internal, ok := session.NetworkAddress["INIP"].(map[string]interface{})
	if !ok || internal["IP"] != int64(3232236743) || internal["PORT"] != int64(3659) {
		t.Fatalf("network address=%#v", session.NetworkAddress)
	}
	if session.NetworkQOS["NATT"] != int64(4) {
		t.Fatalf("network QOS=%#v", session.NetworkQOS)
	}
	session.PersonaID, session.SessionID, session.Persona = 1, 101, "Player"
	setup := gameSetup(newGame(1, session, []*Session{session}, map[interface{}]interface{}{}, 8, 132, 130))
	gameData := setup["GAME"].(map[string]interface{})
	playerData := setup["PROS"].(blaze.TypedList).Items[0].(map[string]interface{})
	if gameData["NQOS"].(map[string]interface{})["NATT"] != int64(4) ||
		playerData["NQOS"].(map[string]interface{})["NATT"] != int64(4) {
		t.Fatalf("setup QOS game=%#v player=%#v", gameData["NQOS"], playerData["NQOS"])
	}
}

func TestMatchmakingPairsSessionsIntoSharedGame(t *testing.T) {
	s := New(nil)
	host := &Session{AccountID: 10, PersonaID: 10, Persona: "Host", outbound: make(chan outboundFrame, 4)}
	joiner := &Session{AccountID: 20, PersonaID: 20, Persona: "Joiner", outbound: make(chan outboundFrame, 4)}
	req := &legacyfire.Frame{TDF: map[string]interface{}{
		"ATTR": map[interface{}]interface{}{"cartier": "1", "playlist": "529909"},
		"PCAP": []interface{}{int64(8), int64(0)},
	}}
	hostMSID := s.queueMatch(host, req)
	if len(s.matchQueue) != 1 || len(s.games) != 0 {
		t.Fatalf("queue=%d games=%d", len(s.matchQueue), len(s.games))
	}
	joinMSID := s.queueMatch(joiner, req)
	if hostMSID == joinMSID || host.GameID == 0 || host.GameID != joiner.GameID {
		t.Fatalf("msid=%d/%d game=%d/%d", hostMSID, joinMSID, host.GameID, joiner.GameID)
	}
	game := s.games[host.GameID]
	if game == nil || game.Host != host || len(game.Members) != 2 {
		t.Fatalf("game=%#v", game)
	}
	joiner.flushNotifications()
	for index, session := range []*Session{host, joiner} {
		added := <-session.outbound
		extended := <-session.outbound
		if added.frame.Header.Command != NotifyUserAdded || extended.frame.Header.Command != NotifyExtendedData {
			t.Fatalf("setup[%d] user notifications=%#x/%#x", index, added.frame.Header.Command, extended.frame.Header.Command)
		}
		item := <-session.outbound
		decoded, consumed := blaze.DecodeTDF(item.frame.Payload)
		if consumed != len(item.frame.Payload) {
			t.Fatalf("setup[%d] decode failed", index)
		}
		roster, ok := decoded["PROS"].([]interface{})
		if !ok || len(roster) != 2 {
			t.Fatalf("setup[%d] roster=%#v", index, decoded["PROS"])
		}
		reason := decoded["REAS"].(blaze.Union).Value.(map[string]interface{})
		if reason["RSLT"] != int64(index) {
			t.Fatalf("setup[%d] reason=%#v", index, reason)
		}
	}
}

func TestQuickMatchJoinsCompatibleRegisteredLobby(t *testing.T) {
	s := New(nil)
	host := &Session{AccountID: 1, PersonaID: 1, SessionID: 1, Persona: "Player", outbound: make(chan outboundFrame, 8)}
	joiner := &Session{AccountID: 2, PersonaID: 2, SessionID: 2, Persona: "Player2", outbound: make(chan outboundFrame, 8)}
	game := &Game{ID: 7, Host: host, Members: []*Session{host}, Attrs: map[interface{}]interface{}{"cartier": "1", "playlist": "529909", "event": "389860"}, Capacity: 8, Topology: 130, State: 130}
	s.games[game.ID] = game
	req := &legacyfire.Frame{TDF: map[string]interface{}{
		"ATTR": map[interface{}]interface{}{"cartier": "1", "playlist": "529909"},
		"NTOP": int64(132),
		"PCAP": []interface{}{int64(8), int64(0)},
	}}
	msid := s.queueMatch(joiner, req)
	if msid == 0 || joiner.GameID != 7 || len(game.Members) != 2 || len(s.matchQueue) != 0 {
		t.Fatalf("msid=%d game=%d members=%d queue=%d", msid, joiner.GameID, len(game.Members), len(s.matchQueue))
	}
	joiner.flushNotifications()
	for index, member := range []*Session{host, joiner} {
		added := <-member.outbound
		extended := <-member.outbound
		if added.frame.Header.Component != ComponentUserSessions || added.frame.Header.Command != NotifyUserAdded || extended.frame.Header.Command != NotifyExtendedData {
			t.Fatalf("member %d peer user notifications=%#x/%#x", index, added.frame.Header.Command, extended.frame.Header.Command)
		}
		addedTDF, _ := blaze.DecodeTDF(added.frame.Payload)
		user := addedTDF["USER"].(map[string]interface{})
		wantPlayerID, wantName := int64(2), "Player2"
		if member == joiner {
			wantPlayerID, wantName = 1, "Player"
		}
		wantAccountID := int64(2)
		if member == joiner {
			wantAccountID = 1
		}
		if user["AID"] != wantAccountID || user["ID"] != wantPlayerID || user["NAME"] != wantName {
			t.Fatalf("member %d peer user=%#v", index, user)
		}
		extendedTDF, _ := blaze.DecodeTDF(extended.frame.Payload)
		wantSessionID := int64(2)
		if member == joiner {
			wantSessionID = 1
		}
		if extendedTDF["USID"] != wantSessionID {
			t.Fatalf("member %d peer user-session id=%#v, want %d", index, extendedTDF["USID"], wantSessionID)
		}
	}
	hostItem := <-host.outbound
	if hostItem.frame.Header.Command != NotifyPlayerJoining {
		t.Fatalf("host notification command=%#x", hostItem.frame.Header.Command)
	}
	hostNote, consumed := blaze.DecodeTDF(hostItem.frame.Payload)
	if consumed != len(hostItem.frame.Payload) || hostNote["GID"] != int64(7) {
		t.Fatalf("host notification=%#v", hostNote)
	}
	joiningPlayer := hostNote["PDAT"].(map[string]interface{})
	if joiningPlayer["PID"] != int64(2) || joiningPlayer["SID"] != int64(1) || joiningPlayer["SLOT"] != int64(0) || joiningPlayer["NAME"] != "Player2" {
		t.Fatalf("joining player=%#v", joiningPlayer)
	}
	if joiningPlayer["STAT"] != int64(2) {
		t.Fatalf("joining player state=%#v, want ACTIVE_CONNECTING", joiningPlayer["STAT"])
	}
	if got := len(host.outbound); got != 0 {
		t.Fatalf("host received %d premature join-completion frames", got)
	}
	item := <-joiner.outbound
	setup, consumed := blaze.DecodeTDF(item.frame.Payload)
	if consumed != len(item.frame.Payload) {
		t.Fatal("invalid join setup")
	}
	gameTDF := setup["GAME"].(map[string]interface{})
	if gameTDF["GID"] != int64(7) || gameTDF["GSTA"] != int64(130) || gameTDF["NTOP"] != int64(130) {
		t.Fatalf("GAME=%#v", gameTDF)
	}
	if _, exists := gameTDF["TCAP"]; exists {
		t.Fatalf("GAME.TCAP must be omitted until encoded as list<TeamCapacity>: %#v", gameTDF["TCAP"])
	}
	hostInfo := gameTDF["PHST"].(map[string]interface{})
	if hostInfo["HPID"] != int64(1) {
		t.Fatalf("joiner host info=%#v", hostInfo)
	}
	if gameTDF["HSES"] != int64(1) {
		t.Fatalf("joiner topology host session=%#v", gameTDF["HSES"])
	}
	roster := setup["PROS"].([]interface{})
	hostPlayer := roster[0].(map[string]interface{})
	joinPlayer := roster[1].(map[string]interface{})
	if hostPlayer["PID"] != int64(1) || hostPlayer["SID"] != int64(0) || hostPlayer["SLOT"] != int64(0) || joinPlayer["PID"] != int64(2) || joinPlayer["SID"] != int64(1) || joinPlayer["SLOT"] != int64(0) {
		t.Fatalf("roster=%#v", roster)
	}
	if hostPlayer["UID"] != int64(1) || joinPlayer["UID"] != int64(2) {
		t.Fatalf("roster user-session IDs=%#v", roster)
	}
	if hostPlayer["STAT"] != int64(4) || joinPlayer["STAT"] != int64(4) {
		t.Fatalf("roster player states=%#v", roster)
	}
	if hostPlayer["NAME"] != "Player" || joinPlayer["NAME"] != "Player2" {
		t.Fatalf("roster names=%#v", roster)
	}
	reason := setup["REAS"].(blaze.Union).Value.(map[string]interface{})
	if reason["MSID"] != msid || reason["RSLT"] != int64(1) || reason["USID"] != int64(2) {
		t.Fatalf("reason=%#v", reason)
	}
	s.broadcastPlayerAttributes(joiner, 7, 2, map[interface{}]interface{}{"ready": "1"})
	for index, member := range []*Session{host, joiner} {
		item := <-member.outbound
		note, _ := blaze.DecodeTDF(item.frame.Payload)
		expectedPID := int64(2)
		if note["PID"] != expectedPID {
			t.Fatalf("member %d attributes=%#v", index, note)
		}
	}
	s.updateMeshConnection(joiner, game.ID, []interface{}{
		map[string]interface{}{"PID": host.PersonaID, "STAT": int64(2)},
	})
	s.updateMeshConnection(host, game.ID, []interface{}{
		map[string]interface{}{"PID": joiner.PersonaID, "STAT": int64(2)},
	})
	if !game.MeshReady[host.PersonaID] || !game.MeshReady[joiner.PersonaID] {
		t.Fatalf("mesh readiness after bidirectional connection=%#v", game.MeshReady)
	}
	host.flushNotifications()
	for index, member := range []*Session{host, joiner} {
		if got := len(member.outbound); got != 1 {
			t.Fatalf("member %d received %d join-completion frames", index, got)
		}
		item := <-member.outbound
		if item.frame.Header.Component != ComponentGameManager ||
			item.frame.Header.Command != NotifyPlayerJoined ||
			item.frame.Header.MessageType != legacyfire.MessageNotify {
			t.Fatalf("member %d join-completion header=%+v", index, item.frame.Header)
		}
		note, consumed := blaze.DecodeTDF(item.frame.Payload)
		if consumed != len(item.frame.Payload) || note["GID"] != game.ID ||
			note["PID"] != joiner.PersonaID {
			t.Fatalf("member %d join-completion=%#v", index, note)
		}
	}
	if joiner.PendingMatchmakingGame != 0 {
		t.Fatalf("joiner pending game=%d after completion", joiner.PendingMatchmakingGame)
	}
}

func TestDecodeUpdateMeshConnectionFixture(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"001b0004001d000000000010" +
			"9e99000001d21ca70403019ac9f30000c299000002cf4874000000")))
	if err != nil {
		t.Fatal(err)
	}
	if req.TDF["GID"] != int64(1) {
		t.Fatalf("mesh request=%#v", req.TDF)
	}
	targets, ok := req.TDF["TARG"].([]interface{})
	if !ok || len(targets) != 1 {
		t.Fatalf("targets=%#v", req.TDF["TARG"])
	}
	target := targets[0].(map[string]interface{})
	if target["PID"] != int64(2) || target["STAT"] != int64(0) {
		t.Fatalf("target=%#v", target)
	}
	s := New(nil)
	target["PID"] = int64(1)
	host := &Session{PersonaID: 1, Persona: "Player", outbound: make(chan outboundFrame, 2)}
	joiner := &Session{PersonaID: 2, Persona: "Player2", outbound: make(chan outboundFrame, 2)}
	s.games[1] = &Game{ID: 1, Host: host, Members: []*Session{host, joiner}, Capacity: 2, State: 130}
	reply, err := s.Handlers[key(ComponentGameManager, CommandUpdateMesh)](context.Background(), joiner, req)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Header.MessageType != legacyfire.MessageReply || reply.Header.MessageID != 16 || len(reply.Payload) != 0 {
		t.Fatalf("reply=%+v", reply)
	}
	if got := s.games[1].Mesh[2][1]; got != 0 {
		t.Fatalf("mesh status=%d", got)
	}

	target["STAT"] = int64(2)
	req.Header.MessageID = 17
	_, err = s.Handlers[key(ComponentGameManager, CommandUpdateMesh)](context.Background(), joiner, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []*Session{host, joiner} {
		if got := len(member.outbound); got != 0 {
			t.Fatalf("one-way mesh completed join for PID %d: %d frames", member.PersonaID, got)
		}
	}
	s.updateMeshConnection(host, 1, []interface{}{
		map[string]interface{}{"PID": int64(2), "STAT": int64(2)},
	})
	if !s.games[1].MeshReady[host.PersonaID] || !s.games[1].MeshReady[joiner.PersonaID] {
		t.Fatalf("mesh readiness=%#v", s.games[1].MeshReady)
	}
	for _, member := range []*Session{host, joiner} {
		if got := len(member.outbound); got != 0 {
			t.Fatalf("mesh readiness sent %d duplicate roster frames to PID %d", got, member.PersonaID)
		}
	}
}

func TestCapturedBidirectionalUpdateMeshConnectionFrames(t *testing.T) {
	host := &Session{PersonaID: 1, Persona: "Player", outbound: make(chan outboundFrame, 2)}
	joiner := &Session{PersonaID: 2, Persona: "Player2", outbound: make(chan outboundFrame, 2)}
	s := New(nil)
	s.games[7] = &Game{
		ID:       7,
		Host:     host,
		Members:  []*Session{host, joiner},
		Capacity: 2,
		State:    130,
	}

	tests := []struct {
		name      string
		wire      string
		session   *Session
		targetID  int64
		messageID uint16
	}{
		{
			name:      "joiner reports host connected",
			wire:      "001b0004001d0000000000139e99000007d21ca70403019ac9f30000c299000001cf4874000200",
			session:   joiner,
			targetID:  1,
			messageID: 19,
		},
		{
			name:      "host reports joiner connected",
			wire:      "001b0004001d0000000000179e99000007d21ca70403019ac9f30000c299000002cf4874000200",
			session:   host,
			targetID:  2,
			messageID: 23,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := legacyfire.Read(bytes.NewReader(mustHex(tc.wire)))
			if err != nil {
				t.Fatal(err)
			}
			if req.Header.Component != ComponentGameManager ||
				req.Header.Command != CommandUpdateMesh ||
				req.Header.MessageID != tc.messageID {
				t.Fatalf("header=%+v", req.Header)
			}
			if req.TDF["GID"] != int64(7) {
				t.Fatalf("request TDF=%#v", req.TDF)
			}
			targets, ok := req.TDF["TARG"].([]interface{})
			if !ok || len(targets) != 1 {
				t.Fatalf("targets=%#v", req.TDF["TARG"])
			}
			target, ok := targets[0].(map[string]interface{})
			if !ok || target["PID"] != tc.targetID || target["STAT"] != int64(2) {
				t.Fatalf("target=%#v", targets[0])
			}
			reply, err := s.Handlers[key(ComponentGameManager, CommandUpdateMesh)](
				context.Background(), tc.session, req)
			if err != nil {
				t.Fatal(err)
			}
			if reply.Header.MessageType != legacyfire.MessageReply ||
				reply.Header.MessageID != tc.messageID || len(reply.Payload) != 0 {
				t.Fatalf("reply=%+v", reply)
			}
		})
	}

	if !s.games[7].MeshReady[host.PersonaID] ||
		!s.games[7].MeshReady[joiner.PersonaID] {
		t.Fatalf("mesh readiness=%#v mesh=%#v", s.games[7].MeshReady, s.games[7].Mesh)
	}
}

func TestGetTelemetryServerReturnsDisabledConfiguration(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"0013000900050000000000078ed863010e2430303030303030303030303000")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Handlers[key(ComponentUtility, CommandGetTelemetry)](
		context.Background(), &Session{}, req)
	if err != nil {
		t.Fatal(err)
	}

	var wire bytes.Buffer
	if err := legacyfire.Write(&wire, got); err != nil {
		t.Fatal(err)
	}
	decoded, err := legacyfire.Read(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Header.MessageType != legacyfire.MessageReply || decoded.Header.MessageID != 7 {
		t.Fatalf("header=%+v", decoded.Header)
	}
	if decoded.TDF["DISA"] != int64(1) || decoded.TDF["PORT"] != int64(0) || decoded.TDF["ADRS"] != "" {
		t.Fatalf("TDF=%#v", decoded.TDF)
	}
}

func TestGameSetupKeepsCanonicalPlayerAndSessionIDsGlobal(t *testing.T) {
	host := &Session{PersonaID: 1, SessionID: 101, Persona: "Player"}
	joiner := &Session{PersonaID: 2, SessionID: 202, Persona: "Player2"}
	game := &Game{ID: 9, Host: host, Members: []*Session{host, joiner}, Attrs: map[interface{}]interface{}{}, Capacity: 2, State: 130}

	for _, tc := range []struct {
		name      string
		recipient *Session
		wantPIDs  []int64
	}{
		{name: "host", recipient: host, wantPIDs: []int64{1, 2}},
		{name: "joiner", recipient: joiner, wantPIDs: []int64{1, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := gameSetup(game)
			gameData := setup["GAME"].(map[string]interface{})
			if gameData["HSES"] != int64(101) {
				t.Fatalf("HSES=%v", gameData["HSES"])
			}
			roster := setup["PROS"].(blaze.TypedList).Items
			for i, value := range roster {
				player := value.(map[string]interface{})
				if player["PID"] != tc.wantPIDs[i] {
					t.Fatalf("player %d PID=%v", i, player["PID"])
				}
				wantUID := []int64{101, 202}[i]
				if player["UID"] != wantUID {
					t.Fatalf("player %d UID=%v, want %d", i, player["UID"], wantUID)
				}
				wantName := []string{"Player", "Player2"}[i]
				if player["NAME"] != wantName {
					t.Fatalf("player %d NAME=%v, want %s", i, player["NAME"], wantName)
				}
			}
		})
	}
}

func TestGameSetupSupportsCanonicalArbitraryPlayerRoster(t *testing.T) {
	players := []*Session{
		{AccountID: 101, PersonaID: 201, SessionID: 301, Persona: "Alpha"},
		{AccountID: 102, PersonaID: 202, SessionID: 302, Persona: "Bravo"},
		{AccountID: 103, PersonaID: 203, SessionID: 303, Persona: "Charlie"},
		{AccountID: 104, PersonaID: 204, SessionID: 304, Persona: "Delta"},
	}
	game := newGame(11, players[0], players, map[interface{}]interface{}{"playlist": "529909"}, 8, 132, 130)

	for _, recipient := range players {
		setup := gameSetup(game)
		gameData := setup["GAME"].(map[string]interface{})
		if gameData["HSES"] != int64(301) {
			t.Fatalf("recipient %d HSES=%v", recipient.PersonaID, gameData["HSES"])
		}
		if gameData["PHST"].(map[string]interface{})["HPID"] != int64(201) ||
			gameData["THST"].(map[string]interface{})["HPID"] != int64(201) {
			t.Fatalf("recipient %d host info=%#v/%#v", recipient.PersonaID, gameData["PHST"], gameData["THST"])
		}
		admins := gameData["ADMN"].(blaze.TypedList).Items
		if len(admins) != 1 || admins[0] != int64(201) {
			t.Fatalf("recipient %d admins=%#v", recipient.PersonaID, admins)
		}
		roster := setup["PROS"].(blaze.TypedList).Items
		if len(roster) != len(players) {
			t.Fatalf("recipient %d roster length=%d", recipient.PersonaID, len(roster))
		}
		for index, value := range roster {
			entry := value.(map[string]interface{})
			want := players[index]
			if entry["PID"] != want.PersonaID || entry["UID"] != want.SessionID ||
				entry["NAME"] != want.Persona || entry["SID"] != int64(index) {
				t.Fatalf("recipient %d roster[%d]=%#v", recipient.PersonaID, index, entry)
			}
			if sessionForPlayerID(game, want.PersonaID) != want {
				t.Fatalf("PID %d did not resolve globally", want.PersonaID)
			}
		}
	}
}

func TestMatchmakingFillsRegisteredGameToCapacity(t *testing.T) {
	s := New(nil)
	host := &Session{AccountID: 1, PersonaID: 1, SessionID: 1, Persona: "Player", GameID: 12, outbound: make(chan outboundFrame, 64)}
	game := newGame(12, host, []*Session{host}, map[interface{}]interface{}{"cartier": "1", "playlist": "529909"}, 4, 130, 130)
	s.games[game.ID] = game
	req := &legacyfire.Frame{TDF: map[string]interface{}{
		"ATTR": map[interface{}]interface{}{"cartier": "1", "playlist": "529909"},
		"NTOP": int64(132), "PCAP": []interface{}{int64(4), int64(0)},
	}}

	for id := int64(2); id <= 4; id++ {
		member := &Session{AccountID: id, PersonaID: id, SessionID: id, Persona: personaName(id), outbound: make(chan outboundFrame, 64)}
		s.queueMatch(member, req)
		if member.GameID != game.ID {
			t.Fatalf("player %d game=%d", id, member.GameID)
		}
	}
	if len(game.Members) != 4 {
		t.Fatalf("members=%d", len(game.Members))
	}
	overflow := &Session{AccountID: 5, PersonaID: 5, SessionID: 5, Persona: "Player5", outbound: make(chan outboundFrame, 64)}
	s.queueMatch(overflow, req)
	if overflow.GameID != 0 || len(game.Members) != 4 || len(s.matchQueue) != 1 {
		t.Fatalf("overflow game=%d members=%d queue=%d", overflow.GameID, len(game.Members), len(s.matchQueue))
	}
}

func TestStableSlotsSurviveMiddlePlayerRemoval(t *testing.T) {
	players := []*Session{
		{PersonaID: 1, SessionID: 11, Persona: "Player"},
		{PersonaID: 2, SessionID: 12, Persona: "Player2"},
		{PersonaID: 3, SessionID: 13, Persona: "Player3"},
		{PersonaID: 4, SessionID: 14, Persona: "Player4"},
	}
	game := newGame(13, players[0], players, map[interface{}]interface{}{}, 4, 132, 130)
	if !game.removeMember(players[1]) {
		t.Fatal("middle player was not removed")
	}
	if game.Slots[3] != 2 || game.Slots[4] != 3 {
		t.Fatalf("survivor slots changed: %#v", game.Slots)
	}
	replacement := &Session{PersonaID: 5, SessionID: 15, Persona: "Player5"}
	if slot, added := game.addMember(replacement); !added || slot != 1 {
		t.Fatalf("replacement slot=%d added=%v", slot, added)
	}
}

func TestCanonicalAttributeAndMeshMatrices(t *testing.T) {
	s := New(nil)
	players := make([]*Session, 4)
	for index := range players {
		id := int64(index + 1)
		players[index] = &Session{AccountID: id, PersonaID: id, SessionID: id + 100, Persona: personaName(id), outbound: make(chan outboundFrame, 16)}
	}
	game := newGame(14, players[0], players, map[interface{}]interface{}{}, 4, 132, 130)
	s.games[game.ID] = game

	s.broadcastPlayerAttributes(players[2], game.ID, 3, map[interface{}]interface{}{"ready": "1"})
	s.broadcastPlayerAttributes(players[0], game.ID, 3, map[interface{}]interface{}{"cop": "1"})
	if attrs := game.PlayerAttrs[3]; attrs["ready"] != "1" || attrs["cop"] != "1" {
		t.Fatalf("merged attributes=%#v", attrs)
	}
	s.broadcastPlayerAttributes(players[2], game.ID, 999, map[interface{}]interface{}{"ready": "0"})
	if game.PlayerAttrs[3]["ready"] != "1" {
		t.Fatalf("unknown target mutated player: %#v", game.PlayerAttrs[3])
	}

	targets := make([]interface{}, 0, 3)
	for _, peerID := range []int64{1, 2, 3} {
		targets = append(targets, map[string]interface{}{"PID": peerID, "STAT": int64(0)})
	}
	s.updateMeshConnection(players[3], game.ID, targets)
	for _, peerID := range []int64{1, 2, 3} {
		if _, ok := game.Mesh[4][peerID]; !ok {
			t.Fatalf("mesh edge 4->%d missing: %#v", peerID, game.Mesh[4])
		}
	}
}

func TestMeshReadinessRequiresEveryPeerForArbitraryRoster(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{name: "two_players", count: 2},
		{name: "three_players", count: 3},
		{name: "four_players", count: 4},
		{name: "eight_players", count: 8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New(nil)
			players := make([]*Session, tc.count)
			for index := range players {
				id := int64(index + 1)
				players[index] = &Session{
					AccountID: id, PersonaID: id, SessionID: id + 100,
					Persona: personaName(id), outbound: make(chan outboundFrame, 8),
				}
			}
			game := newGame(40, players[0], players, map[interface{}]interface{}{}, int64(tc.count), 132, 130)
			s.games[game.ID] = game
			for _, player := range players {
				player.GameID = game.ID
			}

			joining := players[len(players)-1]
			for index, peer := range players[:len(players)-1] {
				s.updateMeshConnection(joining, game.ID, []interface{}{
					map[string]interface{}{"PID": peer.PersonaID, "STAT": int64(2)},
				})
				for _, recipient := range players {
					if got := len(recipient.outbound); got != 0 {
						t.Fatalf("one-way edge %d/%d sent roster frames to recipient %d: %d",
							index+1, len(players)-1, recipient.PersonaID, got)
					}
				}
				s.updateMeshConnection(peer, game.ID, []interface{}{
					map[string]interface{}{"PID": joining.PersonaID, "STAT": int64(2)},
				})
				if index == len(players)-2 {
					continue
				}
				for _, recipient := range players {
					if got := len(recipient.outbound); got != 0 {
						t.Fatalf("completed after only %d/%d bidirectional edges; recipient %d received %d frames",
							index+1, len(players)-1, recipient.PersonaID, got)
					}
				}
				if game.MeshReady[joining.PersonaID] {
					t.Fatalf("mesh ready after only %d/%d bidirectional edges",
						index+1, len(players)-1)
				}
			}

			if !game.MeshReady[joining.PersonaID] {
				t.Fatalf("joining player not mesh-ready after all bidirectional edges: %#v", game.MeshReady)
			}
			for _, recipient := range players {
				if got := len(recipient.outbound); got != 0 {
					t.Fatalf("recipient %d received %d roster frames from mesh readiness", recipient.PersonaID, got)
				}
			}

			lastPeer := players[len(players)-2]
			s.updateMeshConnection(joining, game.ID, []interface{}{
				map[string]interface{}{"PID": lastPeer.PersonaID, "STAT": int64(2)},
			})
			for _, recipient := range players {
				if got := len(recipient.outbound); got != 0 {
					t.Fatalf("duplicate mesh update sent %d more frames to recipient %d", got, recipient.PersonaID)
				}
			}
		})
	}
}

func TestMeshRejectsSelfUnknownAndNonMemberTargets(t *testing.T) {
	s := New(nil)
	players := []*Session{
		{AccountID: 1, PersonaID: 1, SessionID: 101, Persona: "Player", outbound: make(chan outboundFrame, 8)},
		{AccountID: 2, PersonaID: 2, SessionID: 102, Persona: "Player2", outbound: make(chan outboundFrame, 8)},
		{AccountID: 3, PersonaID: 3, SessionID: 103, Persona: "Player3", outbound: make(chan outboundFrame, 8)},
	}
	game := newGame(41, players[0], players, map[interface{}]interface{}{}, 3, 132, 130)
	s.games[game.ID] = game
	for _, player := range players {
		player.GameID = game.ID
	}

	joining := players[2]
	s.updateMeshConnection(joining, game.ID, []interface{}{
		map[string]interface{}{"PID": joining.PersonaID, "STAT": int64(2)},
		map[string]interface{}{"PID": int64(999), "STAT": int64(2)},
	})
	if got := len(game.Mesh[joining.PersonaID]); got != 0 {
		t.Fatalf("invalid targets created mesh edges: %#v", game.Mesh[joining.PersonaID])
	}

	outsider := &Session{AccountID: 99, PersonaID: 99, SessionID: 199, Persona: "Outsider", outbound: make(chan outboundFrame, 8)}
	s.updateMeshConnection(outsider, game.ID, []interface{}{
		map[string]interface{}{"PID": players[0].PersonaID, "STAT": int64(2)},
	})
	if _, exists := game.Mesh[outsider.PersonaID]; exists {
		t.Fatalf("non-member created mesh row: %#v", game.Mesh[outsider.PersonaID])
	}
	if len(game.MeshReady) != 0 {
		t.Fatalf("invalid mesh update changed readiness: %#v", game.MeshReady)
	}
	for _, recipient := range players {
		if got := len(recipient.outbound); got != 0 {
			t.Fatalf("invalid mesh update sent %d frames to recipient %d", got, recipient.PersonaID)
		}
	}
}

func TestPeerRemovalReevaluatesConnectingPlayers(t *testing.T) {
	s := New(nil)
	host := &Session{AccountID: 1, PersonaID: 1, SessionID: 101, Persona: "Player", GameID: 42, outbound: make(chan outboundFrame, 8)}
	joining := &Session{AccountID: 2, PersonaID: 2, SessionID: 102, Persona: "Player2", GameID: 42, outbound: make(chan outboundFrame, 8)}
	departing := &Session{AccountID: 3, PersonaID: 3, SessionID: 103, Persona: "Player3", GameID: 42, outbound: make(chan outboundFrame, 8)}
	game := newGame(42, host, []*Session{host, joining, departing}, map[interface{}]interface{}{}, 3, 132, 130)
	s.games[game.ID] = game
	for _, player := range game.Members {
		s.sessions[player.SessionID] = player
		s.activePersonas[player.PersonaID] = player
	}
	game.Mesh[joining.PersonaID] = map[int64]int64{host.PersonaID: 2}
	game.Mesh[host.PersonaID] = map[int64]int64{joining.PersonaID: 2}

	s.removeSession(departing)
	if !game.MeshReady[joining.PersonaID] {
		t.Fatalf("joining player not mesh-ready after missing peer left: %#v", game.MeshReady)
	}
	for _, recipient := range []*Session{host, joining} {
		if got := len(recipient.outbound); got != 1 {
			t.Fatalf("recipient %d frames=%d, want Removed", recipient.PersonaID, got)
		}
		if item := <-recipient.outbound; item.frame.Header.Command != NotifyPlayerRemoved {
			t.Fatalf("recipient %d first command=%#x", recipient.PersonaID, item.frame.Header.Command)
		}
	}
}

func TestRepeatedMatchmakingReplacesQueuedRequestWithoutSelfMatch(t *testing.T) {
	s := New(nil)
	req := &legacyfire.Frame{TDF: map[string]interface{}{
		"ATTR": map[interface{}]interface{}{"cartier": "1", "playlist": "529909"},
		"NTOP": int64(132), "PCAP": []interface{}{int64(4), int64(0)},
	}}
	player := &Session{AccountID: 1, PersonaID: 1, SessionID: 101, Persona: "Player", outbound: make(chan outboundFrame, 16)}

	firstMSID := s.queueMatch(player, req)
	secondMSID := s.queueMatch(player, req)
	if firstMSID == secondMSID {
		t.Fatalf("matchmaking IDs were reused: %d", firstMSID)
	}
	if len(s.games) != 0 || player.GameID != 0 {
		t.Fatalf("repeated request self-matched: games=%d gameID=%d", len(s.games), player.GameID)
	}
	if len(s.matchQueue) != 1 || s.matchQueue[0].session != player || s.matchQueue[0].msid != secondMSID {
		t.Fatalf("queue=%#v", s.matchQueue)
	}
	if player.QueuedMatchID != secondMSID {
		t.Fatalf("queued match ID=%d, want %d", player.QueuedMatchID, secondMSID)
	}

	peer := &Session{AccountID: 2, PersonaID: 2, SessionID: 102, Persona: "Player2", outbound: make(chan outboundFrame, 16)}
	peerMSID := s.queueMatch(peer, req)
	if peerMSID == secondMSID || len(s.matchQueue) != 0 || len(s.games) != 1 {
		t.Fatalf("peer match result: msid=%d queue=%d games=%d", peerMSID, len(s.matchQueue), len(s.games))
	}
	game := s.games[player.GameID]
	if game == nil || game.Host != player || len(game.Members) != 2 || game.Members[0] != player || game.Members[1] != peer {
		t.Fatalf("matched game=%#v", game)
	}
	if player.QueuedMatchID != 0 || peer.QueuedMatchID != 0 {
		t.Fatalf("stale queued IDs=%d/%d", player.QueuedMatchID, peer.QueuedMatchID)
	}
}

func TestCapacityOneRequestsDoNotCreateInvalidPair(t *testing.T) {
	s := New(nil)
	req := &legacyfire.Frame{TDF: map[string]interface{}{
		"ATTR": map[interface{}]interface{}{"cartier": "1", "playlist": "529909"},
		"NTOP": int64(132), "PCAP": []interface{}{int64(1), int64(0)},
	}}
	first := &Session{PersonaID: 1, SessionID: 101, Persona: "Player", outbound: make(chan outboundFrame, 4)}
	second := &Session{PersonaID: 2, SessionID: 102, Persona: "Player2", outbound: make(chan outboundFrame, 4)}
	s.queueMatch(first, req)
	s.queueMatch(second, req)
	if len(s.games) != 0 || len(s.matchQueue) != 2 || first.GameID != 0 || second.GameID != 0 {
		t.Fatalf("capacity-one pair created: games=%d queue=%d gameIDs=%d/%d",
			len(s.games), len(s.matchQueue), first.GameID, second.GameID)
	}
}

func TestActiveGameMemberCannotRequeueOrJoinAnotherGame(t *testing.T) {
	for _, tc := range []struct {
		name        string
		activeIndex int
	}{
		{name: "host", activeIndex: 0},
		{name: "non_host", activeIndex: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New(nil)
			players := []*Session{
				{AccountID: 1, PersonaID: 1, SessionID: 101, Persona: "Player", outbound: make(chan outboundFrame, 32)},
				{AccountID: 2, PersonaID: 2, SessionID: 102, Persona: "Player2", outbound: make(chan outboundFrame, 32)},
				{AccountID: 3, PersonaID: 3, SessionID: 103, Persona: "Player3", outbound: make(chan outboundFrame, 32)},
			}
			attrs := map[interface{}]interface{}{"cartier": "1", "playlist": "529909"}
			original := newGame(50, players[0], players[:2], attrs, 4, 132, 130)
			other := newGame(51, players[2], players[2:], attrs, 4, 132, 130)
			s.games[original.ID], s.games[other.ID] = original, other
			players[0].GameID, players[1].GameID, players[2].GameID = original.ID, original.ID, other.ID
			active := players[tc.activeIndex]
			req := &legacyfire.Frame{TDF: map[string]interface{}{
				"ATTR": attrs, "NTOP": int64(132), "PCAP": []interface{}{int64(4), int64(0)},
			}}

			s.queueMatch(active, req)
			if active.GameID != original.ID || len(original.Members) != 2 || len(other.Members) != 1 {
				t.Fatalf("active member moved games: gameID=%d original=%d other=%d",
					active.GameID, len(original.Members), len(other.Members))
			}
			if len(s.matchQueue) != 0 || active.QueuedMatchID != 0 {
				t.Fatalf("active member was requeued: queue=%d queuedID=%d", len(s.matchQueue), active.QueuedMatchID)
			}
		})
	}
}

func TestQueuedDisconnectClearsAllMatchmakingState(t *testing.T) {
	s := New(nil)
	departing := &Session{AccountID: 1, PersonaID: 1, SessionID: 101, Persona: "Player", QueuedMatchID: 71, outbound: make(chan outboundFrame, 4)}
	remaining := &Session{AccountID: 2, PersonaID: 2, SessionID: 102, Persona: "Player2", QueuedMatchID: 72, outbound: make(chan outboundFrame, 4)}
	s.sessions[departing.SessionID], s.sessions[remaining.SessionID] = departing, remaining
	s.activePersonas[departing.PersonaID], s.activePersonas[remaining.PersonaID] = departing, remaining
	s.matchQueue = []*matchRequest{
		{session: departing, msid: 70},
		{session: remaining, msid: 72},
		{session: departing, msid: 71},
	}

	s.removeSession(departing)
	if s.sessions[departing.SessionID] != nil || s.sessions[remaining.SessionID] != remaining ||
		s.activePersonas[departing.PersonaID] != nil || s.activePersonas[remaining.PersonaID] != remaining {
		t.Fatalf("sessions=%#v", s.sessions)
	}
	if len(s.matchQueue) != 1 || s.matchQueue[0].session != remaining || s.matchQueue[0].msid != 72 {
		t.Fatalf("queue after disconnect=%#v", s.matchQueue)
	}
	if departing.QueuedMatchID != 0 || departing.PendingMatchmakingGame != 0 || departing.GameID != 0 {
		t.Fatalf("departing matchmaking state=%+v", departing)
	}
}

func TestNonHostDisconnectCleansRegistryAndNotifiesSurvivors(t *testing.T) {
	s := New(nil)
	players := make([]*Session, 4)
	for index := range players {
		id := int64(index + 1)
		players[index] = &Session{
			AccountID: id, PersonaID: id, SessionID: id + 100, Persona: personaName(id),
			outbound: make(chan outboundFrame, 8),
		}
		s.sessions[players[index].SessionID] = players[index]
		s.activePersonas[id] = players[index]
	}
	game := newGame(60, players[0], players, map[interface{}]interface{}{}, 4, 132, 130)
	s.games[game.ID] = game
	for _, player := range players {
		player.GameID = game.ID
		game.PlayerAttrs[player.PersonaID]["ready"] = "1"
		game.Mesh[player.PersonaID] = make(map[int64]int64)
		game.MeshReady[player.PersonaID] = true
		for _, peer := range players {
			if peer != player {
				game.Mesh[player.PersonaID][peer.PersonaID] = 2
			}
		}
	}
	departing := players[2]
	departing.PendingMatchmakingGame = game.ID
	departing.QueuedMatchID = 99

	s.removeSession(departing)
	if s.sessions[departing.SessionID] != nil || s.activePersonas[departing.PersonaID] != nil ||
		s.games[game.ID] != game || game.Host != players[0] || len(game.Members) != 3 {
		t.Fatalf("registry after disconnect: session=%#v persona=%#v game=%#v",
			s.sessions[departing.SessionID], s.activePersonas[departing.PersonaID], s.games[game.ID])
	}
	if departing.GameID != 0 || departing.PendingMatchmakingGame != 0 || departing.QueuedMatchID != 0 {
		t.Fatalf("departing state=%+v", departing)
	}
	for _, state := range []map[int64]int64{game.Slots, game.JoinedAt, game.PlayerStates} {
		if _, exists := state[departing.PersonaID]; exists {
			t.Fatalf("departing PID remains in game state: %#v", state)
		}
	}
	if _, exists := game.PlayerAttrs[departing.PersonaID]; exists {
		t.Fatalf("departing attributes remain: %#v", game.PlayerAttrs)
	}
	if _, exists := game.Mesh[departing.PersonaID]; exists {
		t.Fatalf("departing mesh row remains: %#v", game.Mesh)
	}
	if _, exists := game.MeshReady[departing.PersonaID]; exists {
		t.Fatalf("departing readiness remains: %#v", game.MeshReady)
	}
	for _, survivor := range players {
		if survivor == departing {
			if got := len(survivor.outbound); got != 0 {
				t.Fatalf("departing player received %d frames", got)
			}
			continue
		}
		if _, exists := game.Mesh[survivor.PersonaID][departing.PersonaID]; exists {
			t.Fatalf("survivor %d retains mesh edge to departing player", survivor.PersonaID)
		}
		if survivor.GameID != game.ID {
			t.Fatalf("survivor %d gameID=%d", survivor.PersonaID, survivor.GameID)
		}
		if got := len(survivor.outbound); got != 1 {
			t.Fatalf("survivor %d removal frames=%d", survivor.PersonaID, got)
		}
		removed := <-survivor.outbound
		removedTDF, consumed := blaze.DecodeTDF(removed.frame.Payload)
		if removed.frame.Header.Command != NotifyPlayerRemoved || consumed != len(removed.frame.Payload) ||
			removedTDF["GID"] != game.ID || removedTDF["PID"] != departing.PersonaID || removedTDF["REAS"] != int64(1) {
			t.Fatalf("survivor %d removal=%#v", survivor.PersonaID, removedTDF)
		}
	}
}

func TestHostDisconnectDestroysGameAndNotifiesRemainingMembers(t *testing.T) {
	s := New(nil)
	players := []*Session{
		{AccountID: 1, PersonaID: 1, SessionID: 101, Persona: "Player", outbound: make(chan outboundFrame, 8)},
		{AccountID: 2, PersonaID: 2, SessionID: 102, Persona: "Player2", outbound: make(chan outboundFrame, 8)},
		{AccountID: 3, PersonaID: 3, SessionID: 103, Persona: "Player3", outbound: make(chan outboundFrame, 8)},
	}
	game := newGame(61, players[0], players, map[interface{}]interface{}{}, 3, 132, 130)
	s.games[game.ID] = game
	for _, player := range players {
		s.sessions[player.SessionID] = player
		s.activePersonas[player.PersonaID] = player
		player.GameID = game.ID
		player.PendingMatchmakingGame = game.ID
	}

	host := players[0]
	s.removeSession(host)
	if s.games[game.ID] != nil || s.sessions[host.SessionID] != nil || s.activePersonas[host.PersonaID] != nil {
		t.Fatalf("host game/session remain registered: game=%#v session=%#v persona=%#v",
			s.games[game.ID], s.sessions[host.SessionID], s.activePersonas[host.PersonaID])
	}
	if host.GameID != 0 || host.PendingMatchmakingGame != 0 || host.QueuedMatchID != 0 {
		t.Fatalf("host state=%+v", host)
	}
	for _, survivor := range players[1:] {
		if survivor.GameID != 0 || survivor.PendingMatchmakingGame != 0 || survivor.QueuedMatchID != 0 {
			t.Fatalf("survivor %d stale game state: %+v", survivor.PersonaID, survivor)
		}
		if s.sessions[survivor.SessionID] != survivor || s.activePersonas[survivor.PersonaID] != survivor {
			t.Fatalf("survivor %d missing from session registry", survivor.PersonaID)
		}
		if got := len(survivor.outbound); got != 2 {
			t.Fatalf("survivor %d teardown frames=%d", survivor.PersonaID, got)
		}
		removed := <-survivor.outbound
		gameRemoved := <-survivor.outbound
		removedTDF, consumed := blaze.DecodeTDF(removed.frame.Payload)
		if removed.frame.Header.Command != NotifyPlayerRemoved || consumed != len(removed.frame.Payload) ||
			removedTDF["GID"] != game.ID || removedTDF["PID"] != host.PersonaID {
			t.Fatalf("survivor %d host removal=%#v", survivor.PersonaID, removedTDF)
		}
		gameRemovedTDF, consumed := blaze.DecodeTDF(gameRemoved.frame.Payload)
		if gameRemoved.frame.Header.Command != NotifyGameRemoved || consumed != len(gameRemoved.frame.Payload) ||
			gameRemovedTDF["GID"] != game.ID || gameRemovedTDF["REAS"] != int64(2) {
			t.Fatalf("survivor %d game removal=%#v", survivor.PersonaID, gameRemovedTDF)
		}
	}
}

func TestCreateWalUserSessionReturnsSessionKey(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"0000000100e6000000000008")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Handlers[key(ComponentAuthentication, CommandCreateWalSession)](
		context.Background(), &Session{}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 8 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	var wire bytes.Buffer
	if err := legacyfire.Write(&wire, got); err != nil {
		t.Fatal(err)
	}
	decoded, err := legacyfire.Read(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.TDF["KEY"] != "LOCAL-WAL-SESSION-KEY" {
		t.Fatalf("KEY=%v; TDF=%#v", decoded.TDF["KEY"], decoded.TDF)
	}
}

func TestGetAccountReturnsSyntheticAccountInfo(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"00000001001e00000000000b")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Handlers[key(ComponentAuthentication, CommandGetAccount)](
		context.Background(), &Session{AccountID: 1}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 11 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}

	var wire bytes.Buffer
	if err := legacyfire.Write(&wire, got); err != nil {
		t.Fatal(err)
	}
	decoded, err := legacyfire.Read(&wire)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"CO":   "US",
		"MAIL": "player@localhost",
		"RC":   int64(1),
		"STAS": int64(1),
		"STAT": int64(2),
		"UID":  int64(1),
	}
	for tag, value := range want {
		if decoded.TDF[tag] != value {
			t.Fatalf("%s=%#v, want %#v; TDF=%#v", tag, decoded.TDF[tag], value, decoded.TDF)
		}
	}
}

func TestGetAssociationListsReturnsEmptyTypedList(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"002800190006000000000009" +
			"86ccf40403018afa64090000009ac9f30001b2990003b2eb40010100d39c25000100b2dcc0000000")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Handlers[key(ComponentAssociation, CommandGetLists)](
		context.Background(), &Session{}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 9 {
		t.Fatalf("header=%+v", got.Header)
	}
	want := mustHex("b2d870040300") // LMAP, list<struct>, zero elements
	if !bytes.Equal(got.Payload, want) {
		t.Fatalf("payload=%x want=%x", got.Payload, want)
	}
}

func TestUpdateHardwareFlagsAcknowledgesCapturedRequest(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"00057802000800000000000aa379a70000")))
	if err != nil {
		t.Fatal(err)
	}
	if req.TDF["HWFG"] != int64(0) {
		t.Fatalf("request TDF=%#v", req.TDF)
	}
	got, err := s.Handlers[key(ComponentUserSessions, CommandUpdateHardware)](
		context.Background(), &Session{}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 10 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
}

func TestSubmitOfflineGameReportAcknowledgesCapturedRequest(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"01db001c000200000000000c" +
			"9aece80000c32db40700cb0cb4039e1b650701b1a6bb8b089e1b6503932a7605000301018b3d2d00058b5cf40000" +
			"8e387200008e3d6c00008e4bb400008e8c2d00018e8c3700008e9bad00018e9bb700008eda6c00008edc2c0001" +
			"8edc2d00028edc3500008f2bab00018f2ce800008f3a2d00058f3c2c00018f3c2d00028f3c3500008f4a6d0000" +
			"926d240000926d2d0000929cf4000092da6c0000933d240000933d2d00a6d213a23d2c0001a23d2d0001a23d35" +
			"0000aadcac0001aadcad0002aadcb50000ba98ad000aba9d220000ba9d2d00809f49bada730000badced0032beda" +
			"6c0000bee8e40000bee8ed00bbf603ca18ed0001ca18f70000ca2aec0001ca2aed0002ca2af50000ca38720000ca" +
			"3c2d0002ca3c370000ca3d6c0000ca7b240000ca8c2d0001ca8c370000ca9bad0001ca9bb70000cada6c0000cadc" +
			"2c0001cadc2d0001cadc350000cb2bab0001cb3c2c0001cb3c2d0002cb3c350000cb4a6d00b30dce39240000ce39" +
			"2d00af7dce88f40000cecc2d00a0ee6dd2ea740000d2fba30000d2fbac0000d2fbb20000d2fd300000d2fd3400" +
			"b30dd328ac0001d328ad0002d328b50000d33b300000d33c2d00a0cf24d34cf00000da5cb300010000009f2a6400" +
			"009f4e70010c6f66666c696e6573796e630000")))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Payload) != 475 || req.TDF["FNSH"] != int64(0) {
		t.Fatalf("payload=%d TDF=%#v", len(req.Payload), req.TDF)
	}
	report, ok := req.TDF["RPRT"].(map[string]interface{})
	if !ok || report["GRID"] != int64(0) || report["GTYP"] != "offlinesync" {
		t.Fatalf("RPRT=%#v", req.TDF["RPRT"])
	}
	game, ok := report["GAME"].(blaze.Variable)
	if !ok || game.TdfID != 0x40b769b1 || game.Field != "GAME" {
		t.Fatalf("GAME=%#v", report["GAME"])
	}
	got, err := s.Handlers[key(ComponentGameReporting, CommandSubmitOfflineGame)](
		context.Background(), &Session{AccountID: 1}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 12 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
}

func TestResetDedicatedServerReturnsAssignedGameID(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"00d000040019000000000010" +
			"874d320501010208636172746965720002310009706c61796c6973740007353239393039008b4c2c09000000" +
			"9e3d320101009ee86d0107506c61796572009f3974009c049f4e700101009f5cac010100a2e9740403010297" +
			"8a7003a700000000c2fcb4000000a6ea7003a70000008793c08a18c2fcb4008b390000a67baf0000bb4bf000" +
			"8402c238700400020800c27a64010100c27ce30200c2d8780000c638700000ca7a640000cecbf40000d2586d" +
			"00bfff07d2993800bfff07dafa700000db3d32010b52657461696c2d302e3200")))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Payload) != 208 {
		t.Fatalf("payload=%d", len(req.Payload))
	}
	if req.TDF["GNAM"] != "Player" || req.TDF["VSTR"] != "Retail-0.2" {
		t.Fatalf("request TDF=%#v", req.TDF)
	}
	attributes, ok := req.TDF["ATTR"].(map[interface{}]interface{})
	if !ok || attributes["cartier"] != "1" || attributes["playlist"] != "529909" {
		t.Fatalf("ATTR=%#v", req.TDF["ATTR"])
	}

	s := New(nil)
	session := &Session{AccountID: 1, PersonaID: 1, Persona: "Player"}
	got, err := s.Handlers[key(ComponentGameManager, CommandResetDedicated)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 16 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	decoded, consumed := blaze.DecodeTDF(got.Payload)
	if consumed != len(got.Payload) || decoded["GID"] != int64(1) {
		t.Fatalf("consumed=%d payload=%x TDF=%#v", consumed, got.Payload, decoded)
	}
	if len(session.Notifications) != 1 {
		t.Fatalf("notifications=%d", len(session.Notifications))
	}
	setup := session.Notifications[0]
	if setup.Header.Component != ComponentGameManager || setup.Header.Command != NotifyGameSetup || setup.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("setup header=%+v", setup.Header)
	}
	// The direct full-mesh NTOP=130 representation adds one byte over the old
	// NTOP=0 fixture; no unrelated schema fields should change packet shape.
	if len(setup.Payload) != 440 {
		t.Fatalf("setup payload=%d bytes, want 440", len(setup.Payload))
	}
	setupTDF, consumed := blaze.DecodeTDF(setup.Payload)
	if consumed != len(setup.Payload) {
		t.Fatalf("setup consumed=%d payload=%x TDF=%#v", consumed, setup.Payload, setupTDF)
	}
	game, ok := setupTDF["GAME"].(map[string]interface{})
	if !ok || game["GID"] != int64(1) || game["GSTA"] != int64(1) || game["NTOP"] != int64(130) {
		t.Fatalf("GAME=%#v", setupTDF["GAME"])
	}
	for _, tag := range []string{"TCAP", "TIDS"} {
		if _, exists := game[tag]; exists {
			t.Fatalf("GAME.%s unexpectedly present: %#v", tag, game[tag])
		}
	}
	if _, exists := setupTDF["QUEU"]; exists {
		t.Fatalf("QUEU unexpectedly present: %#v", setupTDF["QUEU"])
	}
	roster, ok := setupTDF["PROS"].([]interface{})
	if !ok || len(roster) != 1 {
		t.Fatalf("PROS=%#v", setupTDF["PROS"])
	}
	player, ok := roster[0].(map[string]interface{})
	if !ok || player["PID"] != int64(1) || player["STAT"] != int64(4) {
		t.Fatalf("player=%#v", roster[0])
	}
	reason, ok := setupTDF["REAS"].(blaze.Union)
	if !ok || reason.ActiveMember != 1 {
		t.Fatalf("REAS=%#v", setupTDF["REAS"])
	}
}

func TestFinalizeGameCreationAcknowledgesUpdate(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"000f0004000f000000000010" +
			"9e99000001e2eba30200e339730200")))
	if err != nil {
		t.Fatal(err)
	}
	if req.TDF["GID"] != int64(1) {
		t.Fatalf("GID=%#v", req.TDF["GID"])
	}
	for _, tag := range []string{"XNNC", "XSES"} {
		blob, ok := req.TDF[tag].([]byte)
		if !ok || len(blob) != 0 {
			t.Fatalf("%s=%#v", tag, req.TDF[tag])
		}
	}

	s := New(nil)
	session := &Session{}
	got, err := s.Handlers[key(ComponentGameManager, CommandFinalizeGame)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 16 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	if len(session.Notifications) != 1 || session.Notifications[0].Header.Command != NotifyPlatformHost {
		t.Fatalf("notifications=%#v", session.Notifications)
	}
	platformTDF, consumed := blaze.DecodeTDF(session.Notifications[0].Payload)
	if consumed != len(session.Notifications[0].Payload) || platformTDF["GID"] != int64(1) || platformTDF["PHST"] != int64(0) {
		t.Fatalf("platform host consumed=%d TDF=%#v", consumed, platformTDF)
	}
}

func TestAdvanceGameStateAcknowledgesAndBroadcastsState(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"000b00040003000000000014" +
			"9e990000019f3d21008202")))
	if err != nil {
		t.Fatal(err)
	}
	if req.TDF["GID"] != int64(1) || req.TDF["GSTA"] != int64(130) {
		t.Fatalf("request TDF=%#v", req.TDF)
	}

	s := New(nil)
	session := &Session{}
	got, err := s.Handlers[key(ComponentGameManager, CommandAdvanceGameState)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 20 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	if len(session.Notifications) != 1 {
		t.Fatalf("notifications=%d", len(session.Notifications))
	}
	state := session.Notifications[0]
	if state.Header.Component != ComponentGameManager || state.Header.Command != NotifyGameState || state.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("state header=%+v", state.Header)
	}
	stateTDF, consumed := blaze.DecodeTDF(state.Payload)
	if consumed != len(state.Payload) || stateTDF["GID"] != int64(1) || stateTDF["GSTA"] != int64(130) {
		t.Fatalf("consumed=%d payload=%x TDF=%#v", consumed, state.Payload, stateTDF)
	}
}

func TestSetPlayerAttributesAcknowledgesAndBroadcastsAttributes(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"003b00040008000000000012" +
			"874d32050101030b636f705f626f756e7479000230000d72616365725f626f756e74790002300006726561647900023100" +
			"9e99000001c299000001")))
	if err != nil {
		t.Fatal(err)
	}
	attributes, ok := req.TDF["ATTR"].(map[interface{}]interface{})
	if !ok || attributes["cop_bounty"] != "0" || attributes["racer_bounty"] != "0" || attributes["ready"] != "1" {
		t.Fatalf("ATTR=%#v", req.TDF["ATTR"])
	}
	if req.TDF["GID"] != int64(1) || req.TDF["PID"] != int64(1) {
		t.Fatalf("request TDF=%#v", req.TDF)
	}

	s := New(nil)
	session := &Session{}
	got, err := s.Handlers[key(ComponentGameManager, CommandSetPlayerAttrs)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 18 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	if len(session.Notifications) != 1 {
		t.Fatalf("notifications=%d", len(session.Notifications))
	}
	note := session.Notifications[0]
	if note.Header.Component != ComponentGameManager || note.Header.Command != NotifyPlayerAttrs || note.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("notification header=%+v", note.Header)
	}
	noteTDF, consumed := blaze.DecodeTDF(note.Payload)
	if consumed != len(note.Payload) || noteTDF["GID"] != int64(1) || noteTDF["PID"] != int64(1) {
		t.Fatalf("consumed=%d payload=%x TDF=%#v", consumed, note.Payload, noteTDF)
	}
	noteAttrs, ok := noteTDF["ATTR"].(map[interface{}]interface{})
	if !ok || noteAttrs["ready"] != "1" {
		t.Fatalf("ATTR=%#v", noteTDF["ATTR"])
	}
}

func TestSetGameAttributesAcknowledgesAndBroadcastsAttributes(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"005d00040007000000000013" +
			"874d3205010106086361727469657200023100066576656e74000230000c696e63617273656c65637400023000" +
			"09706c61796c69737400073532393930390006726f756e64000230000b7669736962696c69747900023300" +
			"9e99000001")))
	if err != nil {
		t.Fatal(err)
	}
	attributes, ok := req.TDF["ATTR"].(map[interface{}]interface{})
	if !ok || attributes["cartier"] != "1" || attributes["event"] != "0" ||
		attributes["incarselect"] != "0" || attributes["playlist"] != "529909" ||
		attributes["round"] != "0" || attributes["visibility"] != "3" {
		t.Fatalf("ATTR=%#v", req.TDF["ATTR"])
	}
	if req.TDF["GID"] != int64(1) {
		t.Fatalf("request TDF=%#v", req.TDF)
	}

	s := New(nil)
	session := &Session{}
	got, err := s.Handlers[key(ComponentGameManager, CommandSetGameAttrs)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 19 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	if len(session.Notifications) != 1 {
		t.Fatalf("notifications=%d", len(session.Notifications))
	}
	note := session.Notifications[0]
	if note.Header.Component != ComponentGameManager || note.Header.Command != NotifyGameAttrs || note.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("notification header=%+v", note.Header)
	}
	noteTDF, consumed := blaze.DecodeTDF(note.Payload)
	if consumed != len(note.Payload) || noteTDF["GID"] != int64(1) {
		t.Fatalf("consumed=%d payload=%x TDF=%#v", consumed, note.Payload, noteTDF)
	}
	noteAttrs, ok := noteTDF["ATTR"].(map[interface{}]interface{})
	if !ok || noteAttrs["playlist"] != "529909" || noteAttrs["visibility"] != "3" {
		t.Fatalf("ATTR=%#v", noteTDF["ATTR"])
	}
}

func TestSetGameSettingsAcknowledgesAndBroadcastsSettings(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"000b00040004000000000017" +
			"9e990000019f3974009004")))
	if err != nil {
		t.Fatal(err)
	}
	if req.TDF["GID"] != int64(1) || req.TDF["GSET"] != int64(272) {
		t.Fatalf("request TDF=%#v", req.TDF)
	}

	s := New(nil)
	session := &Session{}
	got, err := s.Handlers[key(ComponentGameManager, CommandSetGameSettings)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 23 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	if len(session.Notifications) != 1 {
		t.Fatalf("notifications=%d", len(session.Notifications))
	}
	note := session.Notifications[0]
	if note.Header.Component != ComponentGameManager || note.Header.Command != NotifyGameSettings || note.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("notification header=%+v", note.Header)
	}
	noteTDF, consumed := blaze.DecodeTDF(note.Payload)
	if consumed != len(note.Payload) || noteTDF["GID"] != int64(1) || noteTDF["ATTR"] != int64(272) {
		t.Fatalf("consumed=%d payload=%x TDF=%#v", consumed, note.Payload, noteTDF)
	}
}

func TestSetPlayerCapacityAcknowledgesAndBroadcastsCapacity(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"000d0004000500000000001c" +
			"9e99000001c238700400020200")))
	if err != nil {
		t.Fatal(err)
	}
	capacities, ok := req.TDF["PCAP"].([]interface{})
	if !ok || len(capacities) != 2 || capacities[0] != int64(2) || capacities[1] != int64(0) {
		t.Fatalf("PCAP=%#v", req.TDF["PCAP"])
	}
	if req.TDF["GID"] != int64(1) {
		t.Fatalf("request TDF=%#v", req.TDF)
	}

	s := New(nil)
	session := &Session{}
	got, err := s.Handlers[key(ComponentGameManager, CommandSetPlayerCapacity)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 28 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	if len(session.Notifications) != 1 {
		t.Fatalf("notifications=%d", len(session.Notifications))
	}
	note := session.Notifications[0]
	if note.Header.Component != ComponentGameManager || note.Header.Command != NotifyGameCapacity || note.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("notification header=%+v", note.Header)
	}
	noteTDF, consumed := blaze.DecodeTDF(note.Payload)
	if consumed != len(note.Payload) || noteTDF["GID"] != int64(1) {
		t.Fatalf("consumed=%d payload=%x TDF=%#v", consumed, note.Payload, noteTDF)
	}
	noteCaps, ok := noteTDF["CAP"].([]interface{})
	if !ok || len(noteCaps) != 2 || noteCaps[0] != int64(2) || noteCaps[1] != int64(0) {
		t.Fatalf("CAP=%#v", noteTDF["CAP"])
	}
}

func TestRemovePlayerAcknowledgesAndBroadcastsRemoval(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"001b0004000b00000000001f" +
			"8b4c2c090000008eed3800009e99000001c299000001ca58730006")))
	if err != nil {
		t.Fatal(err)
	}
	groupID, ok := req.TDF["BTPL"].(blaze.ObjectID)
	if !ok || groupID.Component != 0 || groupID.Type != 0 || groupID.ID != 0 ||
		req.TDF["CNTX"] != int64(0) ||
		req.TDF["GID"] != int64(1) || req.TDF["PID"] != int64(1) || req.TDF["REAS"] != int64(6) {
		t.Fatalf("request TDF=%#v", req.TDF)
	}

	s := New(nil)
	session := &Session{}
	got, err := s.Handlers[key(ComponentGameManager, CommandRemovePlayer)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 31 || len(got.Payload) != 0 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	if len(session.Notifications) != 1 {
		t.Fatalf("notifications=%d", len(session.Notifications))
	}
	note := session.Notifications[0]
	if note.Header.Component != ComponentGameManager || note.Header.Command != NotifyPlayerRemoved || note.Header.MessageType != legacyfire.MessageNotify {
		t.Fatalf("notification header=%+v", note.Header)
	}
	noteTDF, consumed := blaze.DecodeTDF(note.Payload)
	if consumed != len(note.Payload) || noteTDF["CNTX"] != int64(0) ||
		noteTDF["GID"] != int64(1) || noteTDF["PID"] != int64(1) || noteTDF["REAS"] != int64(6) {
		t.Fatalf("consumed=%d payload=%x TDF=%#v", consumed, note.Payload, noteTDF)
	}
}

func TestStartMatchmakingQueuesCapturedRequest(t *testing.T) {
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"01a80004000d00000000000f" +
			"874d320501010208636172746965720002310009706c61796c6973740007353239393039008b4c2c09000000" +
			"8f2a74038f5cf4038e1cb4038e1cb40001d28b2401127265717569726545786163744d617463680000c2c87903c2c87900b5d740d28b2401127265717569726545786163744d61746368000000" +
			"92e9800392e98000a501009e5bc003d28b2401010000ba1d0003d28b2401096d61746368416e790000c33c8003d28b2401010000ca1bab03d28b24010100" +
			"da1b35000800cb3eb203c2387000bfff07c2da6e000000ce9ea503a73ce70000c238700008c23bb40008c2da6e0001d28b24010a7465737444656361790000" +
			"d2586d03ce4a660000d28b24010100d29900000000da986203d28b24010e686f737456696162696c697479000000935c8000a0b8029ee86d0107506c61796572009f3974009f109f6972010b52657461696c2d302e3200" +
			"a67baf0000b6f9250003bb4bf0008402c21d340501010104636f7000023000c2d8780008c2e9740602da1b3503978a7003a700000000c2fcb4000000a6ea7003a70000008793c08a18c2fcb4008b390000c638700000dafa700002")))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Payload) != 424 || req.TDF["GNAM"] != "Player" {
		t.Fatalf("payload=%d TDF=%#v", len(req.Payload), req.TDF)
	}

	s := New(nil)
	session := &Session{AccountID: 1, PersonaID: 1, Persona: "Player"}
	got, err := s.Handlers[key(ComponentGameManager, CommandStartMatchmaking)](
		context.Background(), session, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 15 {
		t.Fatalf("header=%+v payload=%x", got.Header, got.Payload)
	}
	replyTDF, consumed := blaze.DecodeTDF(got.Payload)
	if consumed != len(got.Payload) || replyTDF["MSID"] != int64(1) {
		t.Fatalf("consumed=%d payload=%x TDF=%#v", consumed, got.Payload, replyTDF)
	}
	if len(session.Notifications) != 0 || session.GameID != 0 || session.PendingMatchmakingGame != 0 {
		t.Fatalf("unmatched request created a private game: session=%+v", session)
	}
	if len(s.games) != 0 || len(s.matchQueue) != 1 || s.matchQueue[0].session != session || session.QueuedMatchID != 1 {
		t.Fatalf("games=%d queue=%#v session=%+v", len(s.games), s.matchQueue, session)
	}
}

func TestListEntitlementsReturnsSyntheticOnlineAccess(t *testing.T) {
	s := New(nil)
	req, err := legacyfire.Read(bytes.NewReader(mustHex(
		"002b0001002000000000000b" +
			"8b5a640000970cee0000970cfa0080019ac86700039eeb33040102084e465331314850000100beeb390000")))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Handlers[key(ComponentAuthentication, CommandListEntitlements)](
		context.Background(), &Session{}, req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.MessageType != legacyfire.MessageReply || got.Header.Error != 0 || got.Header.MessageID != 11 {
		t.Fatalf("header=%+v", got.Header)
	}
	decoded, consumed := blaze.DecodeTDF(got.Payload)
	if consumed < 0 {
		t.Fatalf("invalid payload=%x", got.Payload)
	}
	list, ok := decoded["NLST"].([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("NLST=%#v", decoded["NLST"])
	}
	entitlement, ok := list[0].(map[string]interface{})
	if !ok || entitlement["GNAM"] != "NFS11HP" || entitlement["PRID"] != "nfs-2011-pc" || entitlement["STAT"] != int64(1) || entitlement["TYPE"] != int64(1) {
		t.Fatalf("entitlement=%#v", list[0])
	}
}
