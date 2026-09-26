# IIDX Tracker

[日本語](README.ja.md)

Records your beatmania IIDX plays by relaying the traffic between the game and its server, and shows them in a browser.
It does not replace the server (asphyxia etc.); it sits between the game and the server.

## Usage

1. Extract the zip from [Releases](../../releases) and run `iidx-tracker.exe` (`iidx-tracker` on Linux).
2. Open <http://127.0.0.1:8084/ui/> and, under **Settings**, enter the upstream server (the server the game used to connect to, e.g. `http://192.168.1.10:8083`).
3. Point the game at the tracker (`-url http://127.0.0.1:8084` in spice2x, or `<services>` in ea3-config.xml).
   For a game on another PC, use `http://<IP of the tracker's PC>:8084`.
4. Play as usual.

To show song titles and levels, import `music_data.bin` (`music_omni.bin` for omnimix) on the **Music DB** page.

The web UI has no authentication. Do not expose it outside a trusted LAN.

The web UI is in English and Japanese (switch at the top right). The log follows the OS language; set `"language": "en"` or `"ja"` in `config.json` to pick one.

### Options

| Option | Meaning |
|---|---|
| `--upstream URL` | Upstream server |
| `--listen ADDR` | Listen address (default `0.0.0.0:8084`) |
| `--db PATH` | Where to keep the database |
| `--config PATH` | Settings file (default: `config.json` next to the executable) |

## tracker_link.dll (optional)

A DLL for the game, included in the zip. With it, the tracker also records which music data the game loaded, judgments per key,
FAST/SLOW per judgment and the score rate of each measure. Put it in the game's modules folder and load it with `-k tracker_link.dll` (spice2x).

## Building

- Tracker: `go build` with Go 1.26 or later
- tracker_link.dll: `tracker_link\build.ps1` (needs the Visual Studio C++ tools)
- Release files: `build.ps1`

Releases on GitHub are built by GitHub Actions ([.github/workflows/build.yml](.github/workflows/build.yml)) from the tagged source.
Each release lists its SHA-256 checksums in SHA256SUMS.txt, and `gh attestation verify <file> -R iamiamsub/iidx-tracker`
confirms that a file was built there.

## License

MIT, see [LICENSE](LICENSE). Parts taken from other projects keep their licenses: the kbinxml port
([third_party/kbinxml-LICENSE.txt](third_party/kbinxml-LICENSE.txt)) and the omnifix parts of tracker_link.dll
([tracker_link/LICENSE-omnifix.txt](tracker_link/LICENSE-omnifix.txt)), both MIT. Release packages list everything linked in
THIRD_PARTY_NOTICES.txt.
