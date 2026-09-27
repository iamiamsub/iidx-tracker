# IIDX Tracker

[English](README.md)

beatmania IIDX のプレー記録を、ゲームとサーバーの通信を中継しながら保存して、ブラウザで見られるようにするツール。
サーバー（asphyxia など）の代わりではなく、ゲームとサーバーの間に挟まって動く。

## 使い方

1. [Releases](../../releases) の zip を展開し、`iidx-tracker.exe`（Linux は `iidx-tracker`）を起動する。
2. ブラウザで <http://127.0.0.1:8084/ui/> を開き、**設定** で中継先（今までゲームが接続していたサーバー。例: `http://192.168.1.10:8083`）を入れる。
3. ゲームの接続先をトラッカーに変える（spice2x の `-url http://127.0.0.1:8084`、または ea3-config.xml の `<services>`）。
   別の PC のゲームなら `http://<トラッカーの PC の IP>:8084`。
4. いつも通りプレーする。

曲名やレベルを表示するには、**曲DB** 画面で `music_data.bin`（omnimix なら `music_omni.bin`）を取り込む。

Web 画面には認証がないので、信頼できる LAN の外には公開しないこと。

Web 画面は日本語と英語（右上で切り替え）。ログは OS の言語に合わせる。`config.json` に `"language": "ja"` か `"en"` を書けば固定できる。

### オプション

| オプション | 内容 |
|---|---|
| `--upstream URL` | 中継先 |
| `--listen ADDR` | 待ち受け（既定 `0.0.0.0:8084`） |
| `--db PATH` | データベースの保存先 |
| `--config PATH` | 設定ファイル（既定は実行ファイルと同じフォルダの `config.json`） |

## tracker_link.dll（任意）

zip に同梱している、ゲームに読み込ませる DLL。使っている曲データの報告と、鍵盤ごとの判定・判定ごとの FAST/SLOW・
小節ごとのスコアレートが記録されるようになる。ゲームの modules フォルダに置き、`-k tracker_link.dll`（spice2x）で読み込む。
トラッカーを通して接続しているとき（トラッカーが `services.get` の応答で知らせる）だけ動き、そうでなければゲーム終了まで何もしない。

プレーは、ゲームが報告した曲データを含む曲DB に記録する（omnimix かどうかもこれで決まる）。取り込んだことのない曲データは、
記録されたプレーと一緒に **曲DB** 画面で振り分け待ちになるので、どの曲DB か選ぶ。

[2dxtra](https://github.com/aixxe/2dxtra) も読み込んでいれば、2dxtra が作る譜面（Kiraku・Kichiku・All-Scratch）のプレーも記録する。
2dxtra はこれらをサーバーに送らないので、tracker_link.dll がカードアウト時にまとめて送り、トラッカーは通常の譜面とは別に集計する。
**曲DB** 画面で `2dxtra.sqlite` を取り込むとセットごとの譜面が一覧に出るので、曲一覧で譜面セットを選んで見る。

## 難易度表

曲一覧と譜面の画面に、有志の難易度表でのランクを出す。2026-09-27 に取得してゲームの曲 ID に対応付けたものを
トラッカーに組み込んでいる（`internal/store/difficulty.json`）。

- SP☆12: ノマゲ／ハードの「☆12参考表」
  （[スプレッドシート](https://docs.google.com/spreadsheets/d/e/2PACX-1vSUdp6iuEzE8Z5AL1hkoxzLexp89nJnLQMmICm6_MC0_UjCp1ImZFzabcZkvCpK7mcWvm_2t6iYoJRg/pubhtml)）。
  `B+ / A` はノマゲのランク／ハードのランク、`*` は個人差。
- SP☆11: [SP☆11 wiki](https://w.atwiki.jp/bemani2sp11/) のノマゲ／ハード難易度表（2025-02-22 時点のアーカイブを
  iidx-difficulty-table-checker.nomadblacky.dev 経由で取得）。表示は☆12と同じ。
- DP: [DP非公式難易度表](https://zasa.sakura.ne.jp/dp/)（SNJ@KMZS）。

ランクは各表の作者と投票した人たちのもので、このプロジェクトのライセンスの対象ではない。

## ビルド

- トラッカー: Go 1.26 以降で `go build`
- tracker_link.dll: `tracker_link\build.ps1`（Visual Studio の C++ ツールが必要）
- 配布物一式: `build.ps1`

GitHub のリリースは、タグを付けたソースから GitHub Actions（[.github/workflows/build.yml](.github/workflows/build.yml)）でビルドしている。
各リリースの SHA256SUMS.txt に SHA-256 があり、`gh attestation verify <ファイル> -R iamiamsub/iidx-tracker` で
そのファイルがそこでビルドされたことを確かめられる。

## ライセンス

MIT（[LICENSE](LICENSE)）。他のプロジェクトから取り込んだ部分はそれぞれのライセンスのまま:
kbinxml の移植（[third_party/kbinxml-LICENSE.txt](third_party/kbinxml-LICENSE.txt)）と tracker_link.dll の omnifix 由来の部品
（[tracker_link/LICENSE-omnifix.txt](tracker_link/LICENSE-omnifix.txt)）。どちらも MIT。配布物にはリンクしているものすべての表記を
THIRD_PARTY_NOTICES.txt として入れている。
