# Hot Pursuit 2010 Blaze server

A LAN server for the original PC/DVD release of *Need for Speed: Hot Pursuit* (2010). 
Based on Khysnik's Go BlazeSDK and adds the legacy 12-byte FIRE framing used 
by the game's embedded Blaze 3.05 client.

## Current status

- Legacy FIRE frame reader/writer implemented.
- Shared BlazeSDK TDF codec retained.
- Captured Authentication login request decodes successfully.
- Silent-login, logout, and invalid-password responses implemented.
- Separate ProtoSSL redirector (`42127`) and Blaze (`10013`) listeners are implemented.
- TLS 1.0 RSA/RC4 transport supports suites `0x0004` and `0x0005`.
- Synthetic authentication, user sessions, Autolog bootstrap, shared lobby
  registry, queued matchmaking, and asynchronous notifications are implemented.
- GameManager rosters use one canonical account/persona/session identity per
  client, stable lobby slots, LAN addresses, and P2P mesh state up to the
  lobby's advertised capacity.
- Many non-lobby retail services remain intentionally incomplete.

The executable has obsolete TLS RSA/RC4 suites `0x0004` and `0x0005`. 
The server includes a deliberately narrow TLS 1.0 adapter for those
suites because Go's standard `crypto/tls` cannot accept them.

## Build and test

```powershell
go test ./...
go build ./cmd/hpserver
./hpserver.exe
```

By default an ephemeral self-signed RSA certificate is generated. Use
`-cert server.crt -key server.key` to provide a stable certificate and chain.

## LAN play

On every client machine, update the hosts file:

```text
192.168.1.50 gosredirector.ea.com
192.168.1.50 gosredirector.online.ea.com
192.168.1.50 nfshp-prd2-mp-app-01.ea.com
192.168.1.50 autolog1.ea.com
```

Where `192.168.1.50` is the server host's address. Allow inbound TCP
ports `42127`, `10013`, and `18080` through the emulator host firewall. Allow
the game's peer UDP port `3659` between client machines.

The `PID` supplied by `silentLogin` is treated as the client's preferred
persisted persona. It is reused when available. If another active connection
already owns it, the emulator provisions a fresh account/persona instead. The
assigned ID and generated name (`Player`, `Player2`, and so on) are then kept
identical in FullLogin, UserSessions, and every GameManager roster; mixing
requested and assigned identities across those messages makes the retail
client stall after authentication or misidentify its P2P endpoint.

Verify routing before launching the game:

```powershell
Resolve-DnsName gosredirector.ea.com
Test-NetConnection gosredirector.ea.com -Port 42127
Test-NetConnection nfshp-prd2-mp-app-01.ea.com -Port 10013
Test-NetConnection autolog1.ea.com -Port 18080
```

## Captured protocol

The HP header contains six big-endian `uint16` fields:

```text
payload length, component, command, error, message type, message id
```

Message types are request `0x0000`, reply `0x1000`, and error reply
`0x3000`. Authentication is component `1`; login is command `0x28`.

## Attribution

The TDF+FIRE2 foundation is derived from [Khysnik/BlazeSDK](https://github.com/Khysnik/BlazeSDK) 
under its WTFPL (DO WHAT THE FUCK YOU WANT TO PUBLIC LICENSE).
See `LICENSE`.
