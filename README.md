# go-fmml

🇯🇵 日本語 ｜ 🇬🇧 [English](README.en.md)

[![CI](https://github.com/megaak-soft/go-fmml/actions/workflows/ci.yml/badge.svg)](https://github.com/megaak-soft/go-fmml/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/megaak-soft/go-fmml.svg)](https://pkg.go.dev/github.com/megaak-soft/go-fmml)
[![License: MIT](https://img.shields.io/badge/code%20license-MIT-blue.svg)](LICENSE)
[![Docs License: CC BY 4.0](https://img.shields.io/badge/docs%20license-CC%20BY%204.0-lightgrey.svg)](LICENSE-DOCS)

<p align="left">
  <img src="docs/img/go-fmml_icon.jpg" alt="go-fmml" width="120">
</p>

Go (Golang) 製のFM音源＋PCM音源合成ドライバです。[Ebitengine](https://ebitengine.org/) の
[`oto/v3`](https://github.com/ebitengine/oto) を音声出力に使用し、ゲームプロジェクトへライブラリ
として組み込んで、MML（Music Macro Language）で記述したBGMを非同期に再生することを想定しています。
<p>
  <a href="https://megaak.com/go-fmml/docs/index.html" targer="_blabk">https://megaak.com/go-fmml/docs/index.html</a>
</p>

## 特徴（概要）

- YAMAHA系FM音源チップを参考にしたFMシンセシスコア（最大16パート、1パートあたり最大16音ポリフォ
  ニック）
- 基本波形8種類（W1〜W8）を選択可能なオペレータ
- WAVサンプルを使ったPCM音源パート（ワンショット／ロングの2タイプ、最大16パート）
- 独自MML仕様によるシーケンスデータ記述・再生（最大16パートのFM＋16パートのPCMを時間軸ズレなく
  同期再生）
- マスターステレオリバーブ（Schroeder型・FDN型の2種類、パート単位のセンドレベル対応）
- ボリュームフェードイン／フェードアウト、コンダクターパートによるテンポ変化・ループジャンプ地点
  指定
- スタンダードMIDIファイル（SMF）を本エンジンのMML仕様へ変換するツール `Smf2GoFMML` を同梱

詳しい仕様は [`docs/`](docs/index.html) 配下のドキュメントを参照してください。

## 動作環境

- **Go**: 1.24.0 以上（依存する [`ebitengine/oto/v3`](https://github.com/ebitengine/oto) が要求
  する最低バージョンに準拠）
- **対応OS**: Windows / macOS / Linux / Android / iOS / WebAssembly（音声出力に使用する
  `ebitengine/oto/v3` の対応プラットフォームに準拠）

## ディレクトリ構成

```
go-fmml/
  ├── LICENSE              -- コード用のMITライセンス全文
  ├── LICENSE-DOCS         -- ドキュメント用のCC BY 4.0全文
  ├── README.md            -- 本ファイル
  ├── README.en.md         -- 英語版README
  ├── CONTRIBUTING.md      -- 貢献について（日本語・英語）
  ├── CLAUDE.md            -- 開発ロードマップ・仕様書
  ├── docs/                -- 仕様・使い方ドキュメント（HTML）
  ├── go.mod, go.sum       -- Goモジュール定義
  ├── fmcore/, pcmcore/, player/, fileio/, analyze/, memory/,
  │   common/, const/, reverb/  -- ソースコード（パッケージ）
  ├── cmd/                 -- 動作確認用デモアプリ
  └── tools/               -- 付属ツール（Smf2GoFMML）
```

> **Note**: `go.mod` はリポジトリ直下に配置しています（`go get github.com/megaak-soft/go-fmml/...`
> でのモジュール解決の整合性を保つため）。

## 使い方（概要）

### 自分のGoプロジェクトに導入する場合

```bash
go get github.com/megaak-soft/go-fmml
```

```go
import (
    "github.com/megaak-soft/go-fmml/fileio"
    "github.com/megaak-soft/go-fmml/fmcore"
    "github.com/megaak-soft/go-fmml/player"
)
```

具体的なコードサンプルや `ebitengine/oto/v3` との併用方法は
[`docs/index.html`](docs/index.html) を参照してください。

### 本リポジトリ自体をビルドする場合

```bash
git clone https://github.com/megaak-soft/go-fmml.git
cd go-fmml
go build ./...
```

## デモを試す

実際に音を鳴らして動作を確認できる最小サンプルを [`cmd/mmldemo`](cmd/mmldemo) に同梱しています
（FM音色ファイル・MMLファイルの読み込みから再生までの一連の流れを実装した例です）。

```bash
cd cmd/mmldemo
go run .
```


## 貢献について

このプロジェクトは個人プロジェクトであり、プルリクエストや外部からの貢献は受け付けていません。
ご理解いただけますと幸いです。

詳しくは [`CONTRIBUTING.md`](CONTRIBUTING.md) をご覧ください。

## ライセンス（デュアルライセンス）

go-fmml は、コードとドキュメントで異なるライセンスを採用する **デュアルライセンス** 構成です。

| 対象 | ライセンス | ファイル |
| --- | --- | --- |
| ソースコード（`docs/` を除くリポジトリ全体のGoソースコード） | MIT License | [`LICENSE`](LICENSE) |
| ドキュメント（本README、`CLAUDE.md`、`docs/` 配下のHTML等） | Creative Commons Attribution 4.0 International (CC BY 4.0) | [`LICENSE-DOCS`](LICENSE-DOCS) |

- **コード（MIT）**: 著作権表示とライセンス全文の保持のみを条件に、商用・個人を問わずほぼ無制限に
  利用・改変・再配布が可能です。
- **ドキュメント（CC BY 4.0）**: 著作者のクレジット表示（Attribution）を条件に、商用利用を含め自由
  に共有・翻案が可能です。

Copyright (c) 2026 MEGAAK SOFT (https://megaak.com)

本プロジェクトは音声出力に [`ebitengine/oto/v3`](https://github.com/ebitengine/oto) を利用してい
ます。oto/v3自体のライセンスについては [`docs/license.html`](docs/license.html) の「サードパーティ
ライブラリのライセンス表記」節を参照してください。


## その他
本プロジェクトは Claude Code を用い、仕様書（CLAUDE.md）にエンジン仕様・MML仕様・フェーズ計画を詳細に記述した上で
AIに実装させる、いわゆる「仕様書駆動開発（Spec-Driven Development）」の手法で構築されています。

AIによるコード生成では、学習データに含まれるOSSコードの影響でGPL等コピーレフトライセンスの
コードパターンが意図せず混入する「ライセンス汚染」のリスクが指摘されています。
go-fmml では開発仕様書にライセンス汚染排除のための実装ルールを明記し、
コード生成のたびにAI自身へルール遵守をチェックさせる運用で開発しています。


---
## License

  © 2026 MEGAAK SOFT (https://megaak.com) —
  本ドキュメントは CC BY 4.0　の下でライセンスされています。
  https://creativecommons.org/licenses/by/4.0/deed.ja
