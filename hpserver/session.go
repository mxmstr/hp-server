package hpserver

import (
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	blaze "github.com/local/reorigin-hotpursuit"
	"github.com/local/reorigin-hotpursuit/legacyfire"
)

const personaTokenPrefix = "LOCAL-PC-TOKEN|PNAM="

func syntheticUserSessionExtendedData() map[string]interface{} {

	return map[string]interface{}{
		"ADDR": blaze.Union{ActiveMember: 0x7f},
		"BPS":  "",
		"CTY":  "US",
		"CVAR": blaze.Variable{Value: nil},
		"HWFG": int64(0),
		"PSLM": blaze.TypedList{ElemType: 0, Items: []interface{}{}},
		"QDAT": map[string]interface{}{
			"DBPS": int64(0),
			"NATT": int64(0),
			"UBPS": int64(0),
		},
		"UATT": int64(0),
		"ULST": blaze.TypedList{ElemType: 9, Items: []interface{}{}},
	}

}

func personaNameFromToken(token string) string {

	if !strings.HasPrefix(token, personaTokenPrefix) {
		return ""
	}

	name := strings.TrimSpace(strings.TrimPrefix(token, personaTokenPrefix))
	if name == "" || len(name) > 32 || !utf8.ValidString(name) {
		return ""
	}

	for _, value := range []byte(name) {
		if value < 0x20 || value == 0x7f {
			return ""
		}
	}

	return name

}

func personaName(personaID int64) string {

	if personaID <= 1 {
		return "Player"
	}

	return fmt.Sprintf("Player%d", personaID)

}

// userSessionID is the server-wide connection identity used by PROS.UID,
// NotifyExtendedData.USID, GAME.HSES, and matchmaking setup reasons.
func (session *Session) userSessionID() int64 {

	if session.SessionID != 0 {
		return session.SessionID
	}
	// if session.PersonaID != 0 {
	// 	return session.PersonaID
	// }
	return session.PersonaID

}

func (session *Session) notify(frame *legacyfire.Frame) {

	if session.outbound != nil {
		session.enqueue(outboundFrame{frame: frame})
		return
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	session.Notifications = append(session.Notifications, frame)

}

func (session *Session) flushNotifications() {

	session.mu.Lock()
	frames, delay := session.Notifications, session.NotificationDelay
	session.Notifications, session.NotificationDelay = nil, 0
	session.mu.Unlock()

	for i, frame := range frames {

		d := time.Duration(0)

		if i == 0 {
			d = delay
		}

		if !session.enqueue(outboundFrame{frame: frame, delay: d}) {
			return
		}

	}

}

func (session *Session) enqueue(item outboundFrame) bool {

	if session.outbound == nil {
		return false
	}

	if session.done == nil {
		session.outbound <- item
		return true
	}

	select {
	case session.outbound <- item:
		return true
	case <-session.done:
		return false
	}

}

func (session *Session) close() {

	if session.done != nil {
		session.closeOnce.Do(func() { close(session.done) })
	}

}

func sessionQOS(session *Session) map[string]interface{} {

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.NetworkQOS != nil {
		return session.NetworkQOS
	}

	return map[string]interface{}{
		"DBPS": int64(0),
		"NATT": int64(0),
		"UBPS": int64(0),
	}

}

func sessionIPPair(session *Session) map[string]interface{} {

	session.mu.Lock()
	defer session.mu.Unlock()

	if session.NetworkAddress != nil {
		return session.NetworkAddress
	}

	ip := net.ParseIP("127.0.0.1")
	if tcp, ok := session.RemoteAddr.(*net.TCPAddr); ok && tcp.IP != nil {
		ip = tcp.IP
	}

	ip4 := ip.To4()
	numeric := int64(0x7f000001)
	if ip4 != nil {
		numeric = int64(uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3]))
	}

	return map[string]interface{}{
		"EXIP": map[string]interface{}{
			"IP":   int64(0),
			"PORT": int64(0)},
		"INIP": map[string]interface{}{
			"IP":   numeric,
			"PORT": int64(3659)},
	}

}

func canonicalPlayerID(player *Session) int64 {
	return player.PersonaID
}

func playerName(player *Session) string {

	if player.Persona != "" {
		return player.Persona
	}

	return personaName(player.PersonaID)

}

func userNotificationsFor(player *Session) []*legacyfire.Frame {

	playerID := canonicalPlayerID(player)
	data := syntheticUserSessionExtendedData()
	data["ADDR"] = blaze.Union{
		ActiveMember: 2,
		Field:        "VALU",
		Value:        sessionIPPair(player)}
	data["QDAT"] = sessionQOS(player)

	return []*legacyfire.Frame{
		notification(ComponentUserSessions,
			NotifyUserAdded,
			blaze.EncodeTDF(map[string]interface{}{
				"DATA": data,
				"USER": map[string]interface{}{
					"AID":  player.AccountID,
					"ALOC": int64(0),
					"EXBB": []byte{},
					"EXID": int64(0),
					"ID":   playerID,
					"NAME": playerName(player)},
			})),
		// NotifyExtendedData is keyed by UserSessionId. USID must match the
		// global UID carried by ReplicatedGamePlayer.
		notification(ComponentUserSessions,
			NotifyExtendedData,
			blaze.EncodeTDF(
				map[string]interface{}{
					"DATA": data,
					"USID": player.userSessionID()})),
	}

}
