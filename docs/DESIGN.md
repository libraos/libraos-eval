# libraos-eval design

## Goal

Make prompt changes to LibraOS digital employees measurable. A team should be able to say "version 2 of the Financial DD employee grounds its quotations better than version 1 on these cases, gives no tool-call replies, and still refuses to approve deals", and show the evidence.

## Principles

1. **Deterministic scoring of stored replies.** The model is called once per case. Everything after that (anchoring, validation, text rules) is pure, so `verify` can recompute every score from the stored reply and detect tampering.
2. **One prompt, one name.** An installed prompt's agent name is derived from its bytes, and the run records the prompt digest, case-file digest, evaluator version and model.
3. **Comparable or refused.** Runs are compared only when they share a case file and evaluator version. The comparison says one run per side is one sample.
4. **Fail closed on grounding.** A quotation that is not found in the evidence fails the draft. Anchoring proves that a quote exists in the source, not that it supports the claim, so draft checks complement human review.
5. **No tools by construction and by evidence.** Installed agents use `tool_scope: none` with `triage.default_brain: never`, plus a runtime note against tool-call text. The recording proxy shows what each model request actually carried.
6. **Local by default.** Gateways and the UI are loopback-only unless explicitly allowed.

## Components

| File | Responsibility |
| --- | --- |
| `eval/cases.go` | JSONL case format and validation |
| `eval/anchor.go` | Quote anchoring and exact citation checks (Unicode code points) |
| `eval/validate.go` | Draft normalization and strict validation |
| `eval/score.go` | Per-reply scoring, tool-markup detection, evaluator version |
| `eval/message.go` | The fixed harness message per case |
| `eval/gateway.go` | Gateway client: install a prompt as an agent, submit and poll jobs |
| `eval/run.go` | Execute, seal, verify, load and save run records |
| `eval/compare.go` | Run comparison |
| `eval/install.go` | LibraOS employee bundle (employee + bound agent) |
| `eval/proxy.go` | Recording proxy and tool-exposure summary |
| `eval/report.go`, `report.html.tmpl` | HTML report and local read-only UI |

## Run record

`tool_version`, `evaluator_version`, `label`, timestamps, `gateway`, `agent`, `model`, `prompt_sha256`, `cases_sha256`, the full `cases`, per-case `results` (message sent, reply, job reference, latency, tokens, score, anchored draft, failures), `summary`, and `digest` over all of it.

Changing scoring behavior requires a new `EvaluatorVersion`. Old runs then fail `verify` with a clear message instead of silently re-scoring under new rules.

## Origin

The anchoring and draft-validation rules come from the Investment OS Employee Lab, where a live-model mode evaluates employee candidates inside a Deal-scoped review and publication workflow. `libraos-eval` is the workflow-free core: files in, files out, and any LibraOS gateway.

## Not in 0.1

- Model-graded or rubric scoring. Every check is deterministic.
- Repeated sampling and confidence intervals. Run twice and compare yourself.
- Streaming, managed knowledge collections, and multi-turn cases.
