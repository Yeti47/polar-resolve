# =============================================================================
# polar-resolve Dockerfile
#
# Multi-stage build:
#   Stage 1 (builder): Compiles the Go binary and extracts ORT libraries
#   Stage 2 (runtime): Minimal image with ROCm runtime, ffmpeg, and the binary
#
# Compatibility chain:
#   ROCm 7.1.1 base → ORT 1.23.1 migraphx wheel (ROCm EP) → Go bindings v1.22.0
#   ORT 1.23.1 supports API versions 1-23. Go bindings v1.22.0 requests
#   ORT_API_VERSION 22, which is within that range. Go bindings v1.22.0 is the
#   first version with the generic AppendExecutionProvider() Go wrapper.
# =============================================================================

# ---------------------------------------------------------------------------
# Stage 1: Build
# ---------------------------------------------------------------------------
FROM rocm/dev-ubuntu-22.04:7.1.1 AS builder

# Install Go
ARG GO_VERSION=1.25.5
RUN apt-get update && apt-get install -y --no-install-recommends \
        wget ca-certificates git unzip python3 patchelf && \
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz && \
    tar -C /usr/local -xzf /tmp/go.tar.gz && \
    rm /tmp/go.tar.gz && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"
ENV GOPATH="/root/go"

# Extract ORT shared libraries from the ROCm MIGraphX pip wheel
# ORT 1.23.1 from AMD's ROCm 7.1.1 repo (API version 23 — compatible with Go bindings v1.22.0 requesting version 22)
ARG ORT_WHEEL_URL="https://repo.radeon.com/rocm/manylinux/rocm-rel-7.1.1/onnxruntime_migraphx-1.23.1-cp310-cp310-manylinux_2_27_x86_64.manylinux_2_28_x86_64.whl"
RUN mkdir -p /opt/onnxruntime/lib && \
    cd /tmp && \
    wget -q "${ORT_WHEEL_URL}" -O ort_migraphx.whl && \
    unzip -o ort_migraphx.whl "onnxruntime/capi/*.so*" -d extracted && \
    cp extracted/onnxruntime/capi/*.so* /opt/onnxruntime/lib/ && \
    cd /opt/onnxruntime/lib && \
    # Create the unversioned symlink if it doesn't exist
    if [ ! -f libonnxruntime.so ]; then \
        for f in libonnxruntime.so.1.*; do \
            [ -f "$f" ] && ln -sf "$f" libonnxruntime.so && break; \
        done; \
    fi && \
    # De-mangle auditwheel-patched NEEDED entries (e.g. libamdhip64-9c0f4954.so.7.1.70101 -> libamdhip64.so)
    # so the dynamic linker finds the real ROCm libraries at runtime
    for lib in /opt/onnxruntime/lib/libonnxruntime_providers_*.so; do \
        for needed in $(patchelf --print-needed "$lib" 2>/dev/null); do \
            demangled=$(echo "$needed" | sed -E 's/-[0-9a-f]{8}\.so[.0-9]*/.so/'); \
            if [ "$demangled" != "$needed" ]; then \
                echo "Fixing $lib: $needed -> $demangled"; \
                patchelf --replace-needed "$needed" "$demangled" "$lib"; \
            fi; \
        done; \
    done && \
    ls -la /opt/onnxruntime/lib/ && \
    rm -rf /tmp/ort_migraphx.whl /tmp/extracted

# Build the Go binary
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /polar-resolve ./cmd/polar-resolve/

# ---------------------------------------------------------------------------
# Stage 2: Runtime
# ---------------------------------------------------------------------------
FROM rocm/dev-ubuntu-22.04:7.1.1

# Install ffmpeg, MIGraphX runtime, and minimal runtime dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg \
        ca-certificates \
        migraphx && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

# Copy ORT libraries
COPY --from=builder /opt/onnxruntime/lib/ /opt/onnxruntime/lib/

# Copy the built binary
COPY --from=builder /polar-resolve /usr/local/bin/polar-resolve

# Set up library paths and HSA override for RDNA2 GPUs (RX 6800 = gfx1030)
ENV LD_LIBRARY_PATH="/opt/onnxruntime/lib:/opt/rocm/lib"
ENV HSA_OVERRIDE_GFX_VERSION="10.3.0"

# Default model cache directory inside the container
ENV POLAR_RESOLVE_MODEL_DIR="/models"
RUN mkdir -p /models /workspace

WORKDIR /workspace

ENTRYPOINT ["polar-resolve"]
CMD ["--help"]
