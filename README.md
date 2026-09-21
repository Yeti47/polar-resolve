# polar-resolve

A multi-media toolkit: 4× image and video upscaling powered by
[Real-ESRGAN](https://github.com/xinntao/Real-ESRGAN) (ONNX Runtime) plus
video downloading from YouTube, Twitter/X, and other
[yt-dlp](https://github.com/yt-dlp/yt-dlp)-supported sites. Runs on CPU or AMD
GPU via ROCm/MIGraphX.

## Features

- **Image upscaling** — PNG, JPEG, WebP; single files or glob patterns
- **Video upscaling** — frame-by-frame processing via ffmpeg with audio passthrough
- **Video downloads** — YouTube, Twitter/X, and hundreds of yt-dlp sites, with optional auto-upscaling
- **Web UI** — browser interface for uploads *and* URL downloads with live progress
- **GPU acceleration** — AMD ROCm (MIGraphX execution provider), or CPU-only via the slim `-cpu` image
- **Auto model download** — fetches Real-ESRGAN-General-x4v3 from Qualcomm AI Hub on first run
- **Tiled inference** — processes large images in overlapping tiles with blended seams

## Prerequisites

- Docker with [Compose V2](https://docs.docker.com/compose/) — *or* a published image (see below)
- AMD GPU with ROCm support for GPU mode — tested with RX 6800 (gfx1030); otherwise use the CPU image
- Downloads require `yt-dlp` and `ffmpeg` (both included in the Docker images)

## Setup

### Option A — pull a published image

Two images are published, both tagged `latest`, `vX.Y.Z`/`vX.Y` (on git tags),
and short-SHA:

| Image | Size | Use |
|-------|------|-----|
| `ghcr.io/yeti47/polar-resolve` | ~11 GB | AMD GPU (ROCm/MIGraphX) |
| `ghcr.io/yeti47/polar-resolve-cpu` | ~670 MB | CPU-only, runs anywhere |

The GPU image is large because it bundles the ROCm runtime. If you don't have an
AMD GPU, use the CPU image:

```bash
mkdir -p ~/polar-resolve/input ~/polar-resolve/output

docker run --rm -it \
  -v polar-resolve-models:/models \
  -v ~/polar-resolve/input:/workspace/input \
  -v ~/polar-resolve/output:/workspace/output \
  ghcr.io/yeti47/polar-resolve-cpu:latest --help
```

For AMD GPU acceleration, add the device passthrough flags:

```bash
docker run --rm -it \
  --device /dev/kfd --device /dev/dri \
  --group-add video --group-add render \
  --security-opt seccomp=unconfined \
  -e HSA_OVERRIDE_GFX_VERSION=10.3.0 \
  -e POLAR_RESOLVE_DEVICE=rocm \
  -v polar-resolve-models:/models \
  -v ~/polar-resolve/input:/workspace/input \
  -v ~/polar-resolve/output:/workspace/output \
  ghcr.io/yeti47/polar-resolve:latest --help
```

### Option B — build from source

```bash
git clone https://github.com/Yeti47/polar-resolve.git
cd polar-resolve
docker compose build
```

Create the input/output directories on the host:

```bash
mkdir -p ~/polar-resolve/input ~/polar-resolve/output
```

Place your files in `~/polar-resolve/input/`.

## Usage

### Upscale an image

```bash
docker compose run --rm polar-resolve image \
  --input photo.png \
  --output photo_4x.png
```

### Upscale a video

```bash
docker compose run --rm polar-resolve video \
  --input clip.mp4 \
  --output clip_4x.mp4
```

### Download from YouTube / Twitter

```bash
# Download a YouTube video
docker compose run --rm polar-resolve download \
  "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

# Download a Twitter/X video and 4× upscale it in one step
docker compose run --rm polar-resolve download --upscale \
  "https://x.com/user/status/1234567890"

# Audio only (MP3), capped at the best stream up to 720p
docker compose run --rm polar-resolve download --audio-only -q 720p "https://youtu.be/..."
```

Relative `--output` paths resolve under `/workspace/output`; the default output
name is derived from the video title (with a `_4x` suffix when `--upscale` is used).

| Download flag | Default | Description |
|---------------|---------|-------------|
| `--url`, `-u` | — | Video URL (repeatable; URLs may also be passed as arguments) |
| `--output`, `-o` | `/workspace/output` | Output file or directory |
| `--quality`, `-q` | `best` | Max quality: `best`, `1080p`, `720p`, `480p`, `360p` |
| `--audio-only` | `false` | Extract audio as MP3 instead of downloading video |
| `--cookies-from-browser` | — | Load cookies from a browser (`chrome`, `firefox`, …) for private/age-restricted videos |
| `--upscale` | `false` | 4× upscale the downloaded video |

With `--upscale`, the video flags (`--codec`, `--crf`, `--tile-size`,
`--tile-overlap`, `--no-audio`) apply to the upscaling step.

### Web UI

```bash
docker compose run --rm --service-ports polar-resolve web-ui
# then open http://localhost:8080
```

The web UI has two modes: **Upload file** (image/video upscaling) and
**From URL** (YouTube/Twitter/X downloads, optionally upscaled). A live status
overlay shows model download progress on first start.

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--device` | `auto` | Execution provider: `auto`, `cpu`, `rocm` |
| `--model` | *(auto-download)* | Path to a custom ONNX model |
| `--tile-size` | `128` | Tile size in pixels |
| `--tile-overlap` | `16` | Overlap between tiles in pixels |
| `--codec` | `libx264` | Video codec (`libx264`, `libx265`) |
| `--crf` | `18` | Video quality (lower = better) |
| `--no-audio` | `false` | Remove audio track from the output video |
| `--verbose` | `false` | Enable verbose logging |

### Environment variables

| Variable | Description |
|----------|-------------|
| `POLAR_RESOLVE_DEVICE` | Override `--device` |
| `POLAR_RESOLVE_MODEL_DIR` | Model cache directory (default: `/models` in container) |
| `POLAR_RESOLVE_YTDLP` | Path to a specific `yt-dlp` binary (default: found on `PATH`) |
| `HSA_OVERRIDE_GFX_VERSION` | ROCm GFX version override (set to `10.3.0` for RDNA2) |

## Building from source (without Docker)

Requires Go 1.25+, ONNX Runtime shared libraries, ffmpeg, and yt-dlp (only for
the `download` command).

```bash
CGO_ENABLED=1 go build -o polar-resolve ./cmd/polar-resolve/
```

Set `LD_LIBRARY_PATH` to include your ONNX Runtime library directory.

## Container images

The [publish workflow](.github/workflows/docker-publish.yml) builds and pushes
both images on `v*` tag pushes and on manual dispatch:

- `ghcr.io/yeti47/polar-resolve` — AMD GPU, from `Dockerfile`
- `ghcr.io/yeti47/polar-resolve-cpu` — CPU-only, from `Dockerfile.cpu`

Build either locally:

```bash
docker build -t polar-resolve .                      # ROCm/MIGraphX
docker build -f Dockerfile.cpu -t polar-resolve-cpu . # CPU-only
```

Pin the bundled yt-dlp with `--build-arg YTDLP_VERSION=2026.08.19`; pin the CPU
image's ONNX Runtime with `--build-arg ORT_VERSION=1.23.1`.

## License

[MIT](LICENSE.md)
