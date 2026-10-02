# pokemon-sv-npln

Nextendo NPLN server for Pokémon Scarlet (`0100A3D008C5C000`) and Violet (`01008F6008C5E000`), sharing tenant `t-50e39f8f-lp1`. The client supports version 4.0.0 of both games; native regulation records are supplied privately from the supported game build.

## Current status — 2026-09-27

Two-player internet testing confirmed Union Circle, Link Trade, Surprise Trade and Link Battle. Ranked results persisted after a completed battle; the Singles and Doubles counters are separate. Rental Team publication was confirmed in game.

Tera Raid hosting, board discovery, code/private matching and random-search handling have received server corrections. A complete multiplayer raid on the current build still needs confirmation. Online Competition entry/resources are implemented; full competition battle coverage and wider concurrency remain unverified.

Battle Stadium entry was reproduced with a second player's original save and fixed in the emulator: the terms page must return `/callback/agree`, not just `/callback`. The fix is included as a client source patch; swapping or editing a player's save is unnecessary.


## Build

Build with Go 1.26.6:

```sh
go build -o pokemon-sv-npln .
```

Copy `example.env` into your environment and replace every placeholder. The
server requires your own Nextendo TLS/CA material and account-service secrets.
Ranked regulation loading also requires a legally obtained, decompressed
Pokémon Violet 3.0.1 `main` image supplied through
`VIOLET_RANKED_MAIN_IMAGE`. No game files, keys, certificates or account data
are included.

## Tests

Tests are kept in `Test Files`. The runners create an isolated temporary copy of the backend and run the tests and `go vet` there:

```powershell
powershell -NoProfile -File "Test Files/run-tests.ps1"
```

```sh
sh "Test Files/run-tests.sh"
```

## Client patches

`client patches` contains the Ryujinx-Nextendo changes used during local
development. Apply only patches that match the client revision you are
building. The repository does not include a client binary.

## Mystery Gift cards

`build-violet-op-gifts.ps1` builds five item WC9 cards from a separately
obtained, valid Scarlet/Violet item-card template. It recalculates each WC9
checksum. `install-violet-mystery-gift.ps1` validates card structure,
checksum and duplicate IDs before installing a merged catalog in a selected
Ryujinx profile. The repository includes neither a template nor gift binaries.

Based on [Nextendo Network](https://github.com/NextendoNetwork)'s service
layout and public NPLN protocol definitions. See `LICENSE` for terms.

## License

Released under the **[PolyForm Shield License 1.0.0](LICENSE)**, source-available: read, use,
modify, and self-host, but do not use it to provide a product that competes with Nextendo Network.
