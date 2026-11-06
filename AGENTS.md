# Local AI Dashboard

Native desktop application using Go and Wails v3.0.0. The main frontend uses React, TypeScript, Vite and assistant-ui. Production assets in frontend/dist are embedded in Go. The original monitor is preserved in frontend/public/monitor and embedded alongside chat.

Also exposes a Cobra CLI. `bin/local-ai.exe` is the console binary; `bin/local-ai-dashboard.exe` is the GUI binary. Both use the same backend. Keep CLI commands headless and test argument validation, structured output, and errors.

- Read README.md before changing build or runtime setup.
- Keep hardware collection and Ollama calls in Go. The frontend calls the Wails service bridge.
- Handle unavailable metrics explicitly. Never substitute mock telemetry in the application.
- Detect all NVIDIA GPUs dynamically; never hard-code a card or assume a second card is present.
- Bound subprocess and network calls with timeouts. Keep NVIDIA subprocess windows hidden on Windows.
- Keep inference at local Ollama/FastFlowLM endpoints. Do not add hosted services or credentials without a requested feature.
- Complete implementation and relevant verification, fixing failures caused by changes.
- Run `go test ./...`, `go vet ./...`, and `powershell -File scripts/build.ps1` for backend changes.
- For frontend changes, install dependencies with `cd frontend; pnpm install --frozen-lockfile`, then run `pnpm build`. Test streaming, stop, edit/retry, offline states and saved conversation restoration with `node scripts/chat-ui-smoke.cjs` (requires Playwright and Edge). Never substitute fixture data in production.
- Keep streamed Ollama calls and cancellation in Go; use Wails events to transport chunks to React. Do not send prompts to hosted services.
- Conversation files contain assistant-ui exported repositories, including branches. Preserve this structure and revive message dates when loading. Do not add destructive conversation actions without a requested feature.
- Report commands run, checks that could not run, and remaining limitations.

Additional workspace services:
- Document import/search, artifacts and comparisons are in workspace.go/comparison.go. New desktop features should keep useful Cobra equivalents where practical.
- Bound file sizes, extracted text and document counts. Only use locally imported document IDs in chats; never accept a model-generated filesystem path as an attachment.
- Keep document references in attachment metadata and source references in assistant message metadata so edit/retry and saved threads retain provenance.
- Search is keyword-based; do not call it semantic search or imply whole-document comprehension.
- HTML previews must remain sanitized and sandboxed with scripts/network/navigation disabled. Exports preserve original source only when the user explicitly chooses it.
- Keep model comparisons sequential and coordinate the desktop comparison/chat generation gate. Test cancellation and persistence of partial results.
- Run scripts/features-ui-smoke.cjs in addition to the existing chat/hardware UI checks when these workflows change.
- FastFlowLM uses fastflow.go and OpenAI-compatible SSE. Prefix its model IDs with fastflowlm:: internally to preserve routing across history/CLI/comparisons. Keep unprefixed Ollama IDs backwards compatible.
- Probe providers independently. FastFlowLM must work without Ollama or NVIDIA telemetry. Run scripts/fastflow-ui-smoke.cjs for this case.
- Mark client-estimated throughput with rateBasis=estimated; never present missing model size/load time/NPU metrics as measured zero values.
