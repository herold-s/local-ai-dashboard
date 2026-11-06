# Local AI Dashboard

A local-first Windows desktop workspace built with **Go + Wails v3.0.0**, a **React + assistant-ui** chat interface, and a **Cobra** CLI. React is bundled by Vite and embedded into the Windows executable. End users do not need Node to run the compiled application.

## Features

- Automatically discovers Ollama and FastFlowLM local servers. The chat provider selector filters models; model comparisons can span both providers. FastFlowLM chat retains attachments, document search, sources, artifacts and saved conversations.

- Attach UTF-8 text/code, CSV, JSON, PDF text, and `.docx` documents in chat using the paperclip. Imported documents are retained in a local library.
- Search document contents offline with a BM25-style keyword index. Enable **Search my document library** to retrieve excerpts for chat; source chips open the referenced document.
- Open replies and code blocks in an artifact workspace. Preview/edit Markdown, text, code, static HTML, JSON, and CSV; save locally and export original source, text, or an escaped HTML document through a native Save dialog.
- Compare 2–4 installed local models with the same prompt. Runs are sequential, include generation speed/output/total-time metrics, support Stop, and save full or partial results locally.

- Streaming local Ollama chat with stop generation, Markdown, highlighted code and copy actions.
- Edit messages, retry replies, and browse response branches using assistant-ui.
- Searchable saved conversations, restored across application restarts. Threads and branches are stored locally in `%APPDATA%\LocalAIDashboard\conversations\`.
- Model selection and live VRAM status, with explicit Ollama-offline and no-model states.
- The original GPU/model/benchmark dashboard remains available under **Hardware & models**.

- All connected NVIDIA GPUs: utilization, VRAM, temperature, power, and session utilization graphs.
- Ollama installed models and loaded model status, refreshed every five seconds while visible.
- A bounded local generation benchmark (256 output tokens, temperature 0, seed 42).
- Generation tokens/second, total duration, model load duration, and output text.
- Last 100 completed runs saved to `%APPDATA%\LocalAIDashboard\benchmarks.json`.
- Explicit offline, unavailable metric, and failure states. No fabricated hardware data.

## Requirements

- Windows 10/11 with Microsoft Edge WebView2 Runtime.
- Go 1.26+ toolchain. With an older Go launcher, the scripts set `GOTOOLCHAIN=auto` so Go can download the required toolchain. Initial builds need internet access for Go modules.
- For frontend development: Node.js 22+ and pnpm (this project uses pnpm 11). Run `pnpm install --frozen-lockfile` inside `frontend` before rebuilding the frontend.
- NVIDIA driver and `nvidia-smi` on PATH for hardware telemetry.
- Ollama at `http://127.0.0.1:11434` or FastFlowLM at `http://127.0.0.1:52625`, with a compatible local model available. Hardware monitoring does not require either inference server.

## Run

From the project directory:

If building from source for the first time, install the frontend dependencies:

```powershell
cd frontend
pnpm install --frozen-lockfile
cd ..
```

```powershell
powershell -File scripts/run.ps1
```

Or launch the already-built `bin\local-ai-dashboard.exe` directly. For development, rebuild after editing Go or frontend files. The frontend is embedded at build time.

`scripts/build.ps1` type-checks and builds the frontend, then compiles both executables. `-SkipFrontend` supports a Go-only rebuild using the checked-in `frontend/dist` assets. For frontend-only browser development, use `pnpm dev`; backend calls require the Wails runtime, so a plain browser cannot perform real chat outside the desktop application.

```powershell
powershell -File scripts/build.ps1
go test ./...
go vet ./...
```

The build produces two executables from the same code and Cobra command tree:

- `bin\local-ai.exe`: console executable for terminal commands, pipes, and scripts.
- `bin\local-ai-dashboard.exe`: desktop executable with no console window.

Both launch the desktop with no arguments. Use `local-ai.exe` for CLI output; Windows GUI executables do not provide normal terminal output. An optional `-Console` build flag enables a console on the desktop executable for debugging.

## Command-line usage (Cobra)

```powershell
.\bin\local-ai.exe --help
.\bin\local-ai.exe status
.\bin\local-ai.exe gpu
.\bin\local-ai.exe models --json
.\bin\local-ai.exe benchmark --model 'your-local-model:tag' --prompt 'Write a Go palindrome function.'
.\bin\local-ai.exe history --json
.\bin\local-ai.exe gui
.\bin\local-ai.exe completion powershell
.\bin\local-ai.exe documents import 'C:\path\reference.pdf'
.\bin\local-ai.exe documents search 'deployment requirements' --json
.\bin\local-ai.exe documents list
.\bin\local-ai.exe artifacts list --json
.\bin\local-ai.exe artifacts export ARTIFACT_ID --format source --output 'C:\path\answer.md'
.\bin\local-ai.exe compare --models 'model-a:tag,model-b:tag' --prompt 'Explain Go channels.' --json
```

All data commands accept `--json`. Runtime failures return a nonzero exit code and an error on stderr. `status`, `gpu`, and `models` still print available data on a partial failure. Desktop and CLI share the same benchmark implementation and history file. The CLI does not require opening the desktop window for data commands.

CLI artifact export refuses to overwrite an existing file unless `--force` is supplied. Ctrl+C stops a CLI comparison and saves its partial results. A CLI process has its own generation gate; avoid running it alongside desktop chat if you want exclusive model/GPU use.

The Wails CLI is optional for this deliberately small starter. To inspect platform dependencies with the official CLI:

```powershell
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0
wails3 doctor
```

## How the pieces fit

The mint **L·** icon is shared with the sidebar logo. `build/appicon.svg` is the editable source; `build/appicon.png` and the multi-resolution `build/appicon.ico` are exported assets. The checked-in `rsrc_windows_amd64.syso` embeds the icon and Windows manifest into both executables automatically when Go builds them. Wails also uses this resource for the window/taskbar icon.

After changing the ICO, regenerate the Windows resource with the Wails CLI:

```powershell
wails3 generate syso -arch amd64 -icon build/appicon.ico -manifest build/wails.exe.manifest -out rsrc_windows_amd64.syso
```

- `main.go`: Wails desktop window, service registration and embedded UI.
- `cli.go`: Cobra command tree and human-readable/JSON output.
- `monitor.go`: NVIDIA subprocess collection, Ollama HTTP client and benchmark history.
- `chat.go`: bounded Ollama streaming, request cancellation and local conversation storage.
- `workspace.go`: bounded document parsing, keyword ranking, chat source context, artifact storage and exports.
- `comparison.go`: sequential comparison jobs, cancellation and saved results.
- `workspace_cli.go`: document, artifact and comparison Cobra commands.
- `process_windows.go`: prevents telemetry polling from opening console windows.
- `frontend/src/`: React chat UI and the custom assistant-ui runtime adapter. The owned Button component follows shadcn's Radix/CVA composition and uses app CSS tokens.
- `frontend/public/monitor/`: existing monitoring interface. Vite copies it into the embedded build.
- `frontend/dist/`: production frontend assets, included so Go-only builds can run without Node.
- `frontend/pnpm-lock.yaml`: pinned frontend dependency resolution.
- `monitor_test.go`: parsing, inference timing, persistence and failure tests.
- `AGENTS.md`: project instructions for coding agents.

## Benchmark interpretation

Tokens/second = Ollama's `eval_count / eval_duration` (nanoseconds converted to seconds). It excludes prompt processing and loading. Total duration includes Ollama's overall request work. First/cold runs and already-loaded/warm runs are not directly equivalent. Compare the same model, quantization, prompt and settings; record which GPUs are connected. This measures generation speed, not coding correctness. The 256-token cap may truncate the response.

The backend has a three-minute benchmark timeout and allows one benchmark at a time. There is no cancel button in this starter. GPU polling continues during benchmarks. Cloud-labelled models are excluded. Only use actual local Ollama models for this dashboard.

History contains model responses in plain JSON on your computer. There are no analytics, external fonts or automatic model downloads.

## Next steps

Add configurable context size, cancellation, per-run hardware metadata, export, then repeatable coding tests. Connect the RTX 3070 later: detection automatically includes it without code changes.

## Chat limits and privacy

Chat uses local Ollama (`http://127.0.0.1:11434/api/chat`) or FastFlowLM (the local OpenAI-compatible chat endpoint). Cloud-labelled and cloud-backed models are rejected. Prompts are not sent to a hosted chat service. Model-generated images are not fetched, and response URLs are displayed as text. No API keys or subscriptions are required by the interface.

Each request allows up to 200 messages, 512 KiB of message text, an 8192-token context setting, up to 2048 output tokens, and a ten-minute timeout. Ollama/model context handling can truncate long history; start a new conversation for a clean context. There is one active chat generation at a time. Benchmark jobs are separate: avoid benchmarking while chatting if you want meaningful speed measurements.

Desktop comparisons share the chat generation gate and run one model at a time, up to ten minutes per model and thirty minutes overall. Model output may be truncated at 2048 tokens. Generation tokens/second excludes loading and prompt processing; total seconds includes those costs. This is an answer comparison, not an automatic coding-quality score. At least two distinct installed local models are required. No models are downloaded automatically.

## Documents and artifacts

Each attachment is limited to 10 MiB, extracted text to 512 KiB, PDFs to 200 pages, and the library to 200 documents. Text must be UTF-8. Scanned/image-only PDFs need OCR, which is not implemented. Images, legacy `.doc`, presentations and spreadsheets are not attachment formats in this version. Word/PDF previews show extracted text rather than original page layout.

Search uses local keywords, not embeddings or semantic similarity. It works without Ollama. Chat selects excerpts rather than sending an entire document; attachments always contribute at least one excerpt each. A conversation may reference up to eight attached documents. Source chips show the actual retrieved excerpts; the model's answer and citation accuracy still require review. Removing a composer attachment does not delete the imported library document.

The workspace is stored in `%APPDATA%\LocalAIDashboard\workspace\` under `documents`, `artifacts` and `comparisons`. Extracted document text is retained; original binary files are not copied. Artifact source is retained as plain text. Files are unencrypted local JSON. No document content is sent to a hosted service.

HTML previews are sanitized, rendered in a sandbox with a restrictive content policy, and have scripts, links, forms and remote resources removed. Exporting original HTML preserves the original source. JSON must validate before saving; CSV previews are limited to 200 rows/30 columns while exports retain full source. Code exports use the source language extension where recognized. Artifact export does not yet produce `.docx`, `.pptx`, or `.xlsx` documents.

Conversation JSON includes message text, model-generated reasoning where supplied, and edit/retry branches. It is unencrypted on the local computer. Autosave runs after a short idle interval and when switching threads. The app is a chat client; it does not execute model-supplied commands or provide coding-agent tools.

Chat UI smoke checks (optional development dependency: Playwright plus installed Edge):

```powershell
node scripts/chat-ui-smoke.cjs
node scripts/features-ui-smoke.cjs
```

Wails reference: https://v3.wails.io/getting-started/installation/

## FastFlowLM on supported laptops

Install FastFlowLM, its required AMD drivers/runtime, and a compatible model on the laptop separately. Start its local server with a model you have installed:

```powershell
flm serve YOUR_MODEL_TAG
```

The app probes `http://127.0.0.1:52625/v1/models` and uses streaming `/v1/chat/completions`. Select **FastFlowLM** in the chat provider selector, or use **All providers**. Ollama is optional when FastFlowLM is running. Refresh models after starting/stopping a server.

For a non-default local port, set `LOCAL_AI_FLM_URL` before launching the app (the same setting works in the CLI):

```powershell
$env:LOCAL_AI_FLM_URL = 'http://127.0.0.1:8000/v1'
.\bin\local-ai-dashboard.exe
```

Endpoints are restricted to localhost. This does not connect your desktop to a remote laptop. The app does not install FastFlowLM, download models, change drivers, or launch model servers automatically. Hardware/model compatibility is determined by FastFlowLM on that machine.

CLI model IDs from FastFlowLM use a prefix to avoid collisions with Ollama:

```powershell
.\bin\local-ai.exe models --json
.\bin\local-ai.exe benchmark --model 'fastflowlm::YOUR_MODEL_TAG'
.\bin\local-ai.exe compare --models 'ollama-model:tag,fastflowlm::YOUR_MODEL_TAG' --prompt 'Explain Go channels.' --json
```

FastFlowLM streaming, reasoning text, cancellation, comparisons and benchmarks use its OpenAI-compatible protocol. When completion-token usage is supplied, speed is estimated from first streamed content/reasoning to completion and labelled `~` (`rateBasis: estimated` in JSON). If usage is absent, throughput is unavailable. This estimate is not directly equivalent to Ollama's server-reported generation rate. FastFlowLM load time and model disk size may be unavailable. FastFlowLM context length is configured on its server; this client does not force an Ollama-specific context setting on it.

Hardware monitoring remains NVIDIA-only. No AMD NPU utilization, temperature or memory is fabricated; inference can work on a supported laptop even when NVIDIA telemetry is unavailable.

Reference: https://fastflowlm.com/docs/instructions/server/openapi/
