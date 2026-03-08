# polar-resolve

4× image and video upscaler powered by [Real-ESRGAN](https://github.com/xinntao/Real-ESRGAN) (ONNX Runtime). Runs on CPU or AMD GPU via ROCm/MIGraphX.

## Features

- **Image upscaling** — PNG, JPEG, WebP; single files or glob patterns
- **Video upscaling** — frame-by-frame processing via ffmpeg with audio passthrough
- **GPU acceleration** — AMD ROCm (MIGraphX execution provider)
- **Auto model download** — fetches Real-ESRGAN-General-x4v3 from Qualcomm AI Hub on first run
- **Tiled inference** — processes large images in overlapping tiles with blended seams

## Prerequisites

- Docker with [Compose V2](https://docs.docker.com/compose/)
- AMD GPU with ROCm support (for GPU mode) — tested with RX 6800 (gfx1030)

## Setup

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

### Options

| Flag | Default | Description |
|------|---------|-------------|
| `--device` | `auto` | Execution provider: `auto`, `cpu`, `rocm` |
| `--model` | *(auto-download)* | Path to a custom ONNX model |
| `--tile-size` | `128` | Tile size in pixels |
| `--tile-overlap` | `16` | Overlap between tiles in pixels |
| `--codec` | `libx264` | Video codec (`libx264`, `libx265`) |
| `--crf` | `18` | Video quality (lower = better) |
| `--verbose` | `false` | Enable verbose logging |

### Environment variables

| Variable | Description |
|----------|-------------|
| `POLAR_RESOLVE_DEVICE` | Override `--device` |
| `POLAR_RESOLVE_MODEL_DIR` | Model cache directory (default: `/models` in container) |
| `HSA_OVERRIDE_GFX_VERSION` | ROCm GFX version override (set to `10.3.0` for RDNA2) |

## Building from source (without Docker)

Requires Go 1.25+, ONNX Runtime shared libraries, and ffmpeg.

```bash
CGO_ENABLED=1 go build -o polar-resolve ./cmd/polar-resolve/
```

Set `LD_LIBRARY_PATH` to include your ONNX Runtime library directory.

## License

[MIT](LICENSE.md)
