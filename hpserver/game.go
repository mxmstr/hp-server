package hpserver

import (
	"fmt"
	"time"

	blaze "github.com/local/reorigin-hotpursuit"
)

func newGame(id int64,
	host *Session,
	members []*Session,
	attrs map[interface{}]interface{},
	capacity, topology,
	state int64) *Game {

	game := &Game{
		ID: id, Host: host, Attrs: attrs, Capacity: capacity, Topology: topology,
		State: state, Settings: 284,
		Slots: make(map[int64]int64), JoinedAt: make(map[int64]int64),
		PlayerAttrs: make(map[int64]map[interface{}]interface{}), PlayerStates: make(map[int64]int64),
		Mesh: make(map[int64]map[int64]int64), MeshReady: make(map[int64]bool),
	}

	for _, member := range members {
		game.addMember(member)
	}

	return game

}

func (game *Game) ensureMembershipState() {

	if game.Slots == nil {
		game.Slots = make(map[int64]int64)
	}

	if game.JoinedAt == nil {
		game.JoinedAt = make(map[int64]int64)
	}

	if game.PlayerAttrs == nil {
		game.PlayerAttrs = make(map[int64]map[interface{}]interface{})
	}

	if game.PlayerStates == nil {
		game.PlayerStates = make(map[int64]int64)
	}

	for index, member := range game.Members {

		if _, ok := game.Slots[member.PersonaID]; !ok {
			game.Slots[member.PersonaID] = int64(index)
		}

		if game.JoinedAt[member.PersonaID] == 0 {
			game.JoinedAt[member.PersonaID] = time.Now().Unix()
		}

		if _, ok := game.PlayerStates[member.PersonaID]; !ok {
			game.PlayerStates[member.PersonaID] = 4 // ACTIVE_CONNECTED
		}

	}

}

func (game *Game) addMember(session *Session) (int64, bool) {

	game.ensureMembershipState()

	for _, member := range game.Members {
		if member == session || member.PersonaID == session.PersonaID {
			return game.Slots[member.PersonaID], false
		}
	}

	if game.Capacity > 0 && int64(len(game.Members)) >= game.Capacity {
		return -1, false
	}

	used := make(map[int64]bool, len(game.Slots))
	for _, slot := range game.Slots {
		used[slot] = true
	}

	slot := int64(0)
	for used[slot] {
		slot++
	}

	game.Members = append(game.Members, session)
	game.Slots[session.PersonaID] = slot
	game.JoinedAt[session.PersonaID] = time.Now().Unix()
	game.PlayerAttrs[session.PersonaID] = make(map[interface{}]interface{})
	game.PlayerStates[session.PersonaID] = 4 // ACTIVE_CONNECTED

	return slot, true

}

func (game *Game) removeMember(session *Session) bool {

	for i, member := range game.Members {

		if member != session {
			continue
		}

		game.Members = append(game.Members[:i], game.Members[i+1:]...)
		delete(game.Slots, session.PersonaID)
		delete(game.JoinedAt, session.PersonaID)
		delete(game.PlayerAttrs, session.PersonaID)
		delete(game.PlayerStates, session.PersonaID)
		delete(game.Mesh, session.PersonaID)

		for _, connections := range game.Mesh {
			delete(connections, session.PersonaID)
		}
		delete(game.MeshReady, session.PersonaID)

		return true

	}

	return false

}

func isCompatibleGame(game *Game, request *matchRequest) bool {

	if game.Host == request.session || int64(len(game.Members)) >= game.Capacity ||
		(request.cap > 0 && game.Capacity > request.cap) || game.Topology != request.topology {
		return false
	}

	for _, key := range []interface{}{"playlist", "cartier", "event", "visibility"} {

		requested, hasRequested := request.attrs[key]
		actual, hasActual := game.Attrs[key]

		if hasRequested && (!hasActual || requested != actual) {
			return false
		}

	}

	return true

}

func sessionForPlayerID(game *Game, playerID int64) *Session {

	for _, member := range game.Members {
		if canonicalPlayerID(member) == playerID {
			return member
		}
	}

	return nil

}

func meshReadyForPlayer(game *Game, player *Session) bool {

	connections := game.Mesh[player.PersonaID]

	for _, member := range game.Members {

		if member == player {
			continue
		}

		if connections[member.PersonaID] != 2 || game.Mesh[member.PersonaID][player.PersonaID] != 2 {
			return false
		}

	}

	return true

}

// refreshMeshReadiness maintains the full bidirectional mesh. A pending
// matchmaking player transitions to NotifyPlayerJoinCompleted only after this
// view is ready, matching Blaze's joining -> joined lifecycle.
func refreshMeshReadiness(game *Game) {

	if game.MeshReady == nil {
		game.MeshReady = make(map[int64]bool)
	}

	for _, player := range game.Members {
		game.MeshReady[player.PersonaID] = meshReadyForPlayer(game, player)
	}

}

func gameSetup(registryGame *Game) map[string]interface{} {

	registryGame.ensureMembershipState()
	host := registryGame.Host
	personaID, persona := canonicalPlayerID(host), playerName(host)
	ipPair := sessionIPPair(host)
	hostInfo := map[string]interface{}{
		"HPID": personaID,
		"HSLT": int64(0),
	}

	game := map[string]interface{}{
		"ADMN": blaze.TypedList{ElemType: 0, Items: []interface{}{personaID}},
		"ATTR": registryGame.Attrs,
		"CAP":  blaze.TypedList{ElemType: 0, Items: []interface{}{registryGame.Capacity, int64(0)}},
		"GID":  registryGame.ID,
		"GNAM": persona,
		"GPVH": int64(0),
		"GSET": registryGame.Settings, // invites, join-by-player, host migration, join-in-progress
		"GSID": registryGame.ID,
		"GSTA": registryGame.State,
		"GTYP": "",
		"HNET": blaze.TypedList{ElemType: 3, Items: []interface{}{
			blaze.ArmedStruct{Arm: 2, Fields: ipPair},
		}},
		"HSES": host.userSessionID(),
		"IGNO": false,
		"MCAP": registryGame.Capacity,
		"NQOS": sessionQOS(host),
		// Matchmaking normalizes the retail DirtyCast-failover request to the
		// direct full-mesh topology implemented by this emulator.
		"NTOP": registryGame.Topology,
		"PHST": hostInfo,
		"PSAS": "",
		"QCAP": int64(0),
		"SEED": int64(1),
		"THST": hostInfo,
		"UUID": fmt.Sprintf("LOCAL-GAME-%d", registryGame.ID),
		"VOIP": int64(0),
		"VSTR": "Retail-0.2",
	}

	players := make([]interface{}, 0, len(registryGame.Members))
	admins := []interface{}{personaID}

	for _, member := range registryGame.Members {
		players = append(players, replicatedGamePlayer(registryGame, member))
	}

	game["ADMN"] = blaze.TypedList{ElemType: 0, Items: admins}

	return map[string]interface{}{
		"GAME": game,
		"PROS": blaze.TypedList{ElemType: 3, Items: players},
		"REAS": blaze.Union{
			ActiveMember: 1, // ResetDedicatedServerSetupContext
			Field:        "VALU",
			Value: map[string]interface{}{
				"ERR": int64(0),
			},
		},
	}

}

func replicatedGamePlayer(game *Game, player *Session) map[string]interface{} {

	game.ensureMembershipState()
	playerID := canonicalPlayerID(player)
	displayName := playerName(player)
	playerAttributes := game.PlayerAttrs[player.PersonaID]

	if playerAttributes == nil {
		playerAttributes = map[interface{}]interface{}{}
	}

	return map[string]interface{}{
		"BLOB": []byte{}, "EXID": int64(0), "GID": game.ID, "LOC": int64(0),
		"NAME": displayName, "PID": playerID,
		"NQOS": sessionQOS(player),
		"PATT": playerAttributes,
		"PNET": blaze.Union{ActiveMember: 2, Field: "VALU", Value: sessionIPPair(player)},
		// SID is the roster index; SLOT is the SlotType enum. Both players are
		// public participants (0), occupying SID 0 and SID 1 respectively.
		"SID": game.Slots[player.PersonaID], "SLOT": int64(0), "STAT": game.PlayerStates[player.PersonaID],
		"TEAM": int64(-1), "TIDX": int64(-1), "TIME": game.JoinedAt[player.PersonaID],
		"UGID": blaze.ObjectID{}, "UID": player.userSessionID(),
	}

}
