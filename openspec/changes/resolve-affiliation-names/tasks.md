# Tasks

## 1. Lookups and storage

- [ ] 1.1 Add `Corporation` and `Alliance` lookups to `internal/universe` (`GET /corporations/{id}/`, `GET /alliances/{id}/`) returning name and ticker, backed by a `NameCache` interface the package declares (default in memory, `WithNameCache` option); only successes are stored; existing timeout, user agent, body limit and warn logging. Verify with httptest-backed tests: success, cache hit makes no request, a pre-filled cache makes no request, non-200, bad JSON, timeout, id <= 0, a failure is not stored.
- [ ] 1.2 Add the `names.json` store to `internal/state` (`OpenNames`, `Get`, `Put` writing atomically only for a new or changed entry, missing file starts empty, unparseable file moved to `names.json.corrupt` with a warning, a failed write logged and kept in memory, concurrency-safe). Verify with tests: round trip across reopen, corrupt file handling, no write for an unchanged `Put`, concurrent `Put`s under `-race`.

## 2. Relay enrichment

- [ ] 2.1 Declare `Affiliations` and `WithAffiliations` in `internal/relay`; after classification and filters, fill the victim's and final blow's empty corporation and alliance names and tickers for IDs the payload carries, with one kill's lookups running concurrently under one timeout; wire the resolver with the `names.json` store in `cmd/chainsniffer`. Verify with relay tests using a fake: names filled when missing, payload names untouched and not looked up, no alliance looked up or shown when the payload has no alliance ID, failed lookup leaves the part empty and the kill still posts.
- [ ] 2.2 Document in `README.md` that missing corporation and alliance names are looked up from ESI once and kept in `names.json` in the state directory (safe to delete). Verify by reading the card and state sections.
- [ ] 2.3 Run `nix flake check` and verify it passes.

## 3. Live check

- [ ] 3.1 Run the built binary against the map and confirm a live kill whose payload lacks corporation names shows them in `#kills-in-chain`, that `names.json` gains the entries, and that after a restart the same IDs are not looked up again (debug log).
