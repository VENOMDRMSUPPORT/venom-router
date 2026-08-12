# VENOM ROUTER — MVP Architecture, Readiness & Recovery Plan

**Status:** Proposed technical direction
**Date:** 2026-08-08
**Audience:** Engineering team and any agent continuing the project
**Type:** Technical Architecture, Production-Readiness Decision & MVP Recovery Plan

---

## Purpose & Scope

VENOM ROUTER currently suffers from an interconnected set of problems in its
**Providers / Models / Metadata Discovery** layer. These problems directly
degrade application speed, the accuracy of the model listing, the ease of adding
providers, and overall system stability.

This document proposes a root-cause redesign of that layer rather than another
round of incremental patches. The central recommendation is **not** to replace
VENOM ROUTER with OmniRoute, and **not** to turn VENOM ROUTER into a thin
wrapper over OmniRoute. Instead, OmniRoute is used as a **reference and a source
of proven infrastructure patterns** for the provider/model layer, while the
VENOM layer remains the product and the intelligence.

The target end state is a system that is:

> **Automatic by default, cache-first, registry-driven, provider-aware,
> self-healing, and eventually consistent.**

---

## Relationship to the Current Build & Document Structure

This document has two halves. The **design chapters (Parts I–XIV)** are a design
reference, not a greenfield plan: the current Go desktop build already implements
much of the lower layer they argue for — provider adapters behind a stable
contract, the three-tier routing engine (`venom/lite` / `venom/pro` / `venom/max`),
multi-window atomic quota reservation, scoped circuit breakers with cooldown, and
an OpenAI-compatible public API (`/v1/chat/completions`, `/v1/models`) with the
`venom` request extension. Treat the P0/P1 items in Part XIII as **"verify and
align to this model,"** not "build from zero."

The **readiness chapters (Parts XV–XVIII)** record the grounded production-readiness
decision: which build carries the MVP (Part XV), the concrete still-open gaps
(Part XVI), the phased remediation plan to v1.0 (Part XVII), and the assets and
warnings from the alternate build (Part XVIII). Where the design chapters and the
readiness decision disagree on scope, **the readiness decision is authoritative for
what actually ships.**

---

## Table of Contents

**Part I — Executive Summary & Strategic Direction**
- 1.1 Executive Summary
- 1.2 The Core Architectural Decision
- 1.3 OmniRoute as Reference Implementation, Not Core Dependency
- 1.4 What the MVP Should Not Copy from OmniRoute

**Part II — The Core Problem: Model Metadata**
- 2.1 The Real Problem
- 2.2 Why "UNKNOWN" Appears
- 2.3 From "Unknown" to "Unverified"

**Part III — The Solution: A Canonical Model Registry**
- 3.1 The Venom Model Intelligence Registry (VMIR)
- 3.2 The Metadata Resolution Pipeline
- 3.3 Confidence-Based Metadata
- 3.4 Canonical IDs
- 3.5 Alias Resolution
- 3.6 Family-Level Metadata
- 3.7 Handling Brand-New Models
- 3.8 Capability Probing
- 3.9 Source Reconciliation & Conflicts
- 3.10 Provider Overrides: Pricing
- 3.11 Provider Overrides: Context
- 3.12 Provider Overrides: Capabilities
- 3.13 The Effective Model

**Part IV — Separation of Concerns**
- 4.1 Three Distinct Layers: Provider, Connection, Model
- 4.2 Discovery Is Not Metadata
- 4.3 Static, Runtime, and Derived Intelligence

**Part V — Performance: Cache-First, Non-Blocking Startup**
- 5.1 Never Block Startup on Metadata
- 5.2 Cache-First Reads
- 5.3 Target Performance
- 5.4 Stale-While-Revalidate
- 5.5 Fast Startup Snapshot
- 5.6 The Ideal Read Path
- 5.7 The Ideal Write / Refresh Path

**Part VI — Provider Integration Layer**
- 6.1 Automatic Provider Connection Flow
- 6.2 A Data-Driven Provider Registry
- 6.3 The Provider Adapter Contract
- 6.4 No Provider-Specific Logic in the Core
- 6.5 The OmniRoute Registry/Connection Pattern
- 6.6 Reusing OmniRoute's Provider Registry Data
- 6.7 Adding a New Provider
- 6.8 The Custom OpenAI-Compatible Provider Path

**Part VII — Discovery & Background Synchronization**
- 7.1 Do Not Re-Discover on Every Request
- 7.2 Async, Parallel Discovery
- 7.3 Discovery Budgets
- 7.4 Negative Caching
- 7.5 Per-Type TTLs
- 7.6 A Versioned Registry
- 7.7 Request Deduplication (Singleflight)
- 7.8 Background Workers
- 7.9 Registry Update Automation

**Part VIII — Runtime Intelligence & Resilience**
- 8.1 The In-Memory Routing Graph
- 8.2 Event-Driven Updates
- 8.3 Runtime Health Telemetry
- 8.4 Circuit Breakers
- 8.5 Per-Connection Health
- 8.6 Quota-Aware Routing
- 8.7 Last-Known-Good Path
- 8.8 Retry Policy
- 8.9 Error Normalization
- 8.10 The Fallback Graph
- 8.11 Self-Healing

**Part IX — Routing & Virtual Models**
- 9.1 Virtual Models
- 9.2 `venom/pro`
- 9.3 `venom/max`
- 9.4 Multi-Factor Scoring
- 9.5 Filter Before You Score
- 9.6 Dynamic Candidate Pools
- 9.7 The Routing Hot Path
- 9.8 `venom/pro` Policy Example
- 9.9 `venom/max` Policy Example
- 9.10 Hard Constraints
- 9.11 Task Intelligence
- 9.12 A Local Classifier, Not an LLM Router

**Part X — Data Model**
- 10.1 Overview of Tables
- 10.2 `providers`
- 10.3 `provider_connections`
- 10.4 `canonical_models`
- 10.5 `provider_models`
- 10.6 `connection_models`
- 10.7 `model_capabilities`
- 10.8 `runtime_metrics`

**Part XI — Deployment Shape**
- 11.1 A Modular Monolith, Not Microservices
- 11.2 SQLite for the MVP
- 11.3 MVP Delivery Architecture
- 11.4 Backup & Restore

**Part XII — Testing & Observability**
- 12.1 Provider Contract Test Suite
- 12.2 Registry Tests
- 12.3 Performance Tests
- 12.4 Observability Dashboard
- 12.5 The "Unresolved Models" Metric
- 12.6 Metadata Coverage Score
- 12.7 Enforce Principles as Machine Gates
- 12.8 Live Evidence Over Written Procedure

**Part XIII — MVP Scope, Phasing & Roadmap**
- 13.1 What Not to Do
- 13.2 MVP Scope — P0
- 13.3 MVP Scope — P1
- 13.4 MVP Scope — P2
- 13.5 First Providers for the MVP
- 13.6 The "Fully Automatic" Goal
- 13.7 Immediate Fix: Eliminating UNKNOWN
- 13.8 Immediate Fix: Eliminating Startup/UI Latency
- 13.9 Implementation Phases
- 13.10 Recommended MVP Order
- 13.11 Target Onboarding Experience
- 13.12 Before & After
- 13.13 Key MVP KPIs

**Part XIV — Conclusion & Guiding Principles**
- 14.1 Final Recommendation
- 14.2 The Final Architecture Principle
- 14.3 Recommended Final Architecture
- 14.4 Strategic Summary
- 14.5 The Final Architectural Rule

**Part XV — Production-Readiness Decision**
- 15.1 The Decision
- 15.2 Comparison by the Numbers
- 15.3 Why the Desktop Build
- 15.4 The Honest Counterpoint & the Decisive Point

**Part XVI — MVP Gap Analysis**
- 16.1 Critical Gaps (Release-Blocking)
- 16.2 Important Gaps (Weaken the Release)

**Part XVII — Execution Plan to v1.0**
- 17.1 M1 — Close Open Debt
- 17.2 M2 — Activate the Empty Gates
- 17.3 M3 — Resolve Bifrost
- 17.4 M4 — Dated Live Evidence
- 17.5 M5 — Close V1 Scope & Harden
- 17.6 Recommended Order

**Part XVIII — Migration Assets & Final Warnings**
- 18.1 What to Extract from the F Build Before Freezing
- 18.2 Rotate the F Build's Secrets Now
- 18.3 Two Final Warnings

---

# Part I — Executive Summary & Strategic Direction

## 1.1 Executive Summary

The current symptoms in the Providers / Models / Metadata layer are:

- Severe slowness when fetching model information.
- Over-reliance on live discovery operations during runtime.
- Some models surface with values such as `UNKNOWN`.
- No unified, trustworthy metadata across all models.
- Data shape differs from one provider to another.
- Difficulty determining, per model:
  - Context Window
  - Max Output Tokens
  - Pricing
  - Capabilities
  - Tool Calling
  - Vision
  - Reasoning
  - Streaming
  - Structured Outputs
- Likelihood that discovery is re-implemented in more than one place.
- No single, authoritative source of truth for a model.
- Increased time to add a new account or provider.
- Manual work required whenever model names change or new models appear.
- Difficulty building a reliable Smart Router on top of incomplete data.

The proposed goal is to redesign this layer so the system becomes **automatic by
default, cache-first, registry-driven, provider-aware, self-healing, and
eventually consistent** — leveraging the architectural ideas OmniRoute has
already proven instead of re-inventing every provider-management component.

OmniRoute itself has become a large-scale project; its provider reference (July
2026 edition) records roughly **290 providers**, generated automatically from a
central registry rather than maintained by hand.

## 1.2 The Core Architectural Decision

**We do not make OmniRoute *be* VENOM ROUTER, and we do not make VENOM ROUTER a
mere front-end over OmniRoute.**

The preferred architecture is layered:

```text
                     VENOM ROUTER
┌────────────────────────────────────────────────────────┐
│                                                        │
│                 VENOM INTELLIGENCE                     │
│                                                        │
│     venom/max        venom/pro        venom/lite       │
│         │                │                │            │
│         └────────────────┼────────────────┘            │
│                          │                             │
│                Smart Routing Engine                    │
│                          │                             │
│      Task / Context / Capability Intelligence          │
│                          │                             │
├──────────────────────────┼─────────────────────────────┤
│                          │                             │
│              Provider Runtime Layer                    │
│                          │                             │
│     Registry / Discovery / Accounts / Health           │
│     Quotas / Metadata / Models / Fallback              │
│                          │                             │
└──────────────────────────┬─────────────────────────────┘
                            │
        ┌───────────────────┼────────────────────┐
        ▼                   ▼                    ▼
     OpenAI             Anthropic             Google
        ▼                   ▼                    ▼
      Kimi                 GLM               MiniMax
        ...
```

OmniRoute should primarily inform the **lower layer**:

- Provider Registry architecture
- Model Registry architecture
- Provider adapters
- Credential detection
- OAuth patterns
- Model discovery
- Auto-routing concepts
- Health scoring
- Circuit breakers
- Quota awareness
- Fallback
- Model aliases
- Virtual models
- Provider capability mapping
- Cached runtime state

While the following must remain **VENOM-owned**:

- The product tiers (`venom/lite`, `venom/pro`, `venom/max`)
- The Venom Task Classifier
- Venom Model Intelligence
- Routing policies
- Quality policies
- User-facing UX
- Model ranking
- Semantic task detection
- Advanced multi-model orchestration (future)

## 1.3 OmniRoute as Reference Implementation, Not Core Dependency

The recommended strategy is to use OmniRoute **aggressively as a reference**, and
**not** to rebuild VENOM around OmniRoute's APIs:

```text
VENOM Core
   │
   ├── port concepts that solve a concrete problem
   │
   ├── adapt Registry data
   │
   ├── reuse algorithms where appropriate
   │
   └── do NOT embed OmniRoute wholesale
```

Recommended usage:

```text
Use OmniRoute as:
  reference implementation
  provider knowledge source
  architecture source
  algorithm source
  test-case source
```

Not recommended (for now):

```text
rewriting VENOM around OmniRoute APIs
or
turning OmniRoute into the permanent core dependency
```

The reason: if VENOM becomes a wrapper, its architecture is permanently tied to
OmniRoute's. What we actually want to own is:

```text
Venom routing intelligence
Venom UX
Venom virtual models
Venom data model
```

**Why this is better for the MVP.** OmniRoute already provides a working proof of
several problems VENOM would otherwise solve from scratch:

- Hundreds of providers represented through a central registry
- Dynamic provider-connection discovery
- Credential-aware candidate selection
- Virtual auto-combos
- Zero-config routing
- Health-aware routing
- Quota-aware routing
- Cost-aware routing
- Latency-aware routing
- Task-fit routing
- Context-aware selection
- Circuit-breaker integration
- Self-healing candidate exclusion
- Last-known-good routing
- Multi-account awareness

There is no justification for re-inventing these concepts from zero.

## 1.4 What the MVP Should Not Copy from OmniRoute

OmniRoute has grown into a very large gateway, so we should **not** attempt to
port everything. For the MVP we do **not** need:

```text
290 providers
every auth strategy
all media providers
all search providers
all routing strategies
all agent providers
all compression mechanisms
all dashboard functionality
```

We take the **architecture**, not the **scale**.

---

# Part II — The Core Problem: Model Metadata

## 2.1 The Real Problem

The core issue is not that VENOM cannot reach the models. The issue is trying to
obtain **all of a model's truth from the provider at runtime**. That is the wrong
design.

Some APIs return only:

```json
{
  "id": "some-model"
}
```

and provide none of:

```text
context window
max output
pricing
vision
tools
reasoning
structured outputs
release family
input types
```

Another provider returns part of it. A third has no good `models` endpoint at
all. A fourth returns deployment names, not model names. A fifth returns aliases.
A sixth relies on OAuth or a different internal endpoint. Therefore:

> A provider's API must **NOT** be treated as the canonical metadata database.

## 2.2 Why "UNKNOWN" Appears

`UNKNOWN` should not be a permanent model type. Its appearance usually means the
system discovered a `model_id` but failed somewhere in the chain:

```text
model_id
   ↓
canonicalization
   ↓
registry lookup
   ↓
metadata resolution
```

The fix is not to hide `UNKNOWN`. The fix is to build a **Metadata Resolution
Pipeline** (see §3.2).

## 2.3 From "Unknown" to "Unverified"

Instead of:

```text
Name: UNKNOWN
Context: UNKNOWN
Capabilities: UNKNOWN
```

the model should be represented as:

```text
Model:
some-provider/new-model-x

Status:
Detected

Metadata:
Partially verified

Context:
Not verified

Capabilities:
Text confirmed
Streaming confirmed
Tools unverified
Vision unverified
```

This is a large difference. The model genuinely exists; what is unknown is *some
metadata*, not the model's existence.

---

# Part III — The Solution: A Canonical Model Registry

## 3.1 The Venom Model Intelligence Registry (VMIR)

We introduce a unified registry that is the primary source of truth for all model
information. Working name:

```text
Venom Model Intelligence Registry (VMIR)
```

(alternatively: **Model Knowledge Registry**). It holds a **Canonical Record**
per model, for example:

```json
{
  "canonical_id": "anthropic/claude-opus-4.1",

  "provider_model_ids": {
    "anthropic": "claude-opus-4-1",
    "openrouter": "anthropic/claude-opus-4.1",
    "other-provider": "claude-opus-latest"
  },

  "display_name": "Claude Opus 4.1",

  "vendor": "Anthropic",

  "family": "claude-opus",

  "capabilities": {
    "text": true,
    "vision": true,
    "tools": true,
    "reasoning": true,
    "streaming": true,
    "structured_output": true
  },

  "limits": {
    "context": 200000,
    "max_output": 32000
  },

  "pricing": {
    "input": null,
    "output": null
  },

  "routing": {
    "coding": 0.95,
    "reasoning": 0.98,
    "planning": 0.97,
    "debugging": 0.94,
    "speed": 0.61
  },

  "metadata": {
    "source": "registry",
    "confidence": 0.99,
    "last_verified": "..."
  }
}
```

This data must **not** be fetched from the internet when opening the Models page,
when opening the app, or on every request.

## 3.2 The Metadata Resolution Pipeline

When a model such as `some-new-model-2026` is discovered, run:

```text
RAW MODEL
   │
   ▼
Normalize ID
   │
   ▼
Alias Lookup
   │
   ▼
Exact Registry Lookup
   │
   ▼
Provider Mapping Lookup
   │
   ▼
Family Detection
   │
   ▼
Pattern Matching
   │
   ▼
Provider Metadata Merge
   │
   ▼
Safe Inference
   │
   ▼
Canonical Model
```

Recommended source priority:

```text
1. Exact verified registry metadata
2. Provider-specific maintained mapping
3. Official provider response
4. Known alias
5. Model-family inheritance
6. Pattern recognition
7. Conservative fallback
```

`UNKNOWN` is **not** used as a final answer.

## 3.3 Confidence-Based Metadata

Every metadata field must carry its source and a confidence score, e.g.:

```json
{
  "context_window": {
    "value": 262144,
    "source": "registry",
    "confidence": 1.0
  }
}
```

```json
{
  "tool_calling": {
    "value": true,
    "source": "runtime_probe",
    "confidence": 0.95
  }
}
```

```json
{
  "max_output": {
    "value": null,
    "source": "unknown",
    "confidence": 0
  }
}
```

This lets the Smart Router distinguish between `false` and `not known` — a
fundamental difference.

## 3.4 Canonical IDs

A core problem in aggregators is that the *same* model appears under different
names. We need a canonical identifier:

```text
Canonical:
anthropic/claude-sonnet-X
```

with per-provider mappings:

```text
anthropic:
claude-sonnet-x

openrouter:
anthropic/claude-sonnet-x

provider-y:
claude-sonnet-latest
```

The Smart Router deals with the canonical model; the Provider Adapter translates
`canonical → provider model ID`.

## 3.5 Alias Resolution

There must be an alias layer:

```text
Alias
 ↓
Canonical Model
```

For example:

```text
claude-sonnet-latest
→ anthropic/claude-sonnet-X

gpt-latest
→ openai/gpt-X
```

VENOM may define its own aliases — the public product tiers:

```text
venom/lite
venom/pro
venom/max
```

but these do **not** point to a single model — they point to a **routing
policy** (tier).

## 3.6 Family-Level Metadata

Family inheritance is one of the most important ways to eliminate `UNKNOWN`. For
example:

```text
Family:
claude-sonnet

Default family capabilities:
text = true
tools = true
streaming = true
```

If `claude-sonnet-new-version` appears, it may safely inherit:

```text
text
streaming
vendor
family
```

but must **not** assume:

```text
exact context
exact pricing
exact max output
```

unless verified.

## 3.7 Handling Brand-New Models

If a provider adds, say, `kimi-k99-preview` before a registry update arrives,
VENOM automatically:

```text
discover model
     ↓
normalize
     ↓
family detection
     ↓
inherit safe family metadata
     ↓
mark unverified fields
     ↓
add to routing with conservative policy
```

instead of falling back to `UNKNOWN`.

## 3.8 Capability Probing

For information a provider does not clearly expose, a limited runtime probe may be
used, e.g.:

```text
Supports tools?
Supports image input?
Supports JSON schema?
```

But **we do not probe every model on every startup**. Instead:

```text
once
→ cache result
→ attach evidence
→ long TTL
```

## 3.9 Source Reconciliation & Conflicts

When two sources disagree:

```text
Registry says 128K
Provider says 256K
```

do not overwrite silently. Record a `metadata conflict` and apply source
priority, which varies by field:

```text
official verified
>
maintained registry
>
provider API
>
family inference
```

## 3.10 Provider Overrides: Pricing

Pricing can differ per provider even for the same model. Model metadata must
therefore not hold a single global price, but per-provider prices:

```text
Model
  │
  ├── OpenAI price
  ├── OpenRouter price
  └── Provider X price
```

This is important for the Router.

## 3.11 Provider Overrides: Context

A canonical model may support `200K`, but a provider proxy may only allow `128K`.
So:

```text
effectiveContext =
min(modelContext, providerOverride)
```

## 3.12 Provider Overrides: Capabilities

Likewise for capabilities: the base model supports tools, but a specific provider
may not pass tools through. We therefore need:

```text
Canonical capability
+
Provider capability override
+
Connection/runtime capability
```

## 3.13 The Effective Model

Before routing, compute:

```text
EffectiveModel =
CanonicalMetadata
+ ProviderOverrides
+ ConnectionAvailability
+ RuntimeState
```

This is the unit the Router evaluates.

---

# Part IV — Separation of Concerns

## 4.1 Three Distinct Layers: Provider, Connection, Model

Three concepts must be kept separate. **Provider**:

```text
Anthropic
OpenAI
Google
OpenRouter
Z.AI
Kimi
```

**Connection / Account**:

```text
OpenAI Account #1
OpenAI Account #2
OpenAI API Key
Claude OAuth #1
Claude OAuth #2
```

**Model**:

```text
GPT-x
Claude Opus
Claude Sonnet
Kimi Kx
```

The common mistake is coupling `Model = Account = Provider`, which causes
duplication and complicates discovery. The correct design:

```text
Provider
   │
   ├── Connection A
   ├── Connection B
   └── Connection C

Model Registry
   │
   ├── Model A
   ├── Model B
   └── Model C

ConnectionModelAvailability
   │
   ├── Account A → Model A
   ├── Account A → Model C
   └── Account B → Model B
```

## 4.2 Discovery Is Not Metadata

The two operations must be separated. **Discovery** answers:

> Which models can this account access?

```text
GET provider/models
```

**Metadata** answers:

> What are the properties of `claude-x`?

This comes from the Registry. Therefore:

```text
Provider Discovery
        +
Model Intelligence Registry
        ↓
AvailableModel
```

## 4.3 Static, Runtime, and Derived Intelligence

Three kinds of intelligence must not be stored together in an unstructured way.

**Static**:

```text
model family
context
vision
tools
pricing
vendor
capabilities
```

**Runtime**:

```text
latency
quota
health
errors
availability
account status
```

**Derived**:

```text
coding score
reasoning score
value score
speed score
```

---

# Part V — Performance: Cache-First, Non-Blocking Startup

## 5.1 Never Block Startup on Metadata

This is a mandatory architectural rule. On startup VENOM ROUTER should do:

```text
Application Start
      │
      ├── Load local Registry
      ├── Load local DB
      ├── Load last model snapshot
      └── READY
```

then, in the background:

```text
Background Sync
      │
      ├── refresh providers
      ├── refresh models
      ├── refresh quotas
      └── reconcile registry
```

and **never**:

```text
Application Start

wait OpenAI...
wait Claude...
wait Gemini...
wait Kimi...
wait OpenRouter...
wait metadata...
wait context...
wait prices...

READY
```

This is one of the most important changes for making VENOM fast.

## 5.2 Cache-First Reads

Every query from the Models UI must come from a **local DB / memory cache**, not
the provider:

```text
UI
 │
 ▼
Venom API
 │
 ▼
Local Model Store
 │
 ▼
instant response
```

with discovery happening in the background:

```text
Provider
   │
   ▼
Discovery Worker
   │
   ▼
Reconciliation
   │
   ▼
Local Model Store
```

## 5.3 Target Performance

MVP targets:

```text
Open Models page:
< 100 ms local query target

Application startup:
does not depend on provider discovery

Add known provider:
UI becomes usable immediately after authentication

Model list:
render from cache immediately

Discovery:
async

Metadata enrichment:
async

Quota refresh:
async
```

The user should never feel an enrichment operation.

## 5.4 Stale-While-Revalidate

The ideal design resembles:

```text
Request model information

        │
        ▼
Is cached metadata available?
       / \
     yes  no
     /     \
 return     minimal record
 cached         │
 instantly      ▼
           enqueue refresh
     │
     ▼
is cache old?
     │
   yes
     │
refresh async
```

Even if metadata is a day old, showing it immediately beats waiting several
seconds for an API.

## 5.5 Fast Startup Snapshot

On app close, save a `routing snapshot`. On startup:

```text
read registry
read snapshot
hydrate memory
READY
```

then verify in the background. This makes even a cold start very fast.

## 5.6 The Ideal Read Path

```text
GET /models
   ↓
ModelService
   ↓
Memory Cache
   ↓
Local DB if miss
   ↓
return
```

No provider API.

## 5.7 The Ideal Write / Refresh Path

```text
Provider API
    ↓
Provider Adapter
    ↓
Discovery Result
    ↓
Normalization
    ↓
Canonical Resolution
    ↓
Metadata Reconciliation
    ↓
Local DB
    ↓
Invalidate Memory Entry
    ↓
Emit models.updated
```

---

# Part VI — Provider Integration Layer

## 6.1 Automatic Provider Connection Flow

Connecting a provider should become:

```text
Add Provider
     │
     ▼
Authenticate
     │
     ▼
Validate credential
     │
     ▼
Save connection
     │
     ├──────────────► immediately usable
     │
     ▼
Async Discovery
     │
     ▼
Models discovered
     │
     ▼
Registry matching
     │
     ▼
Capabilities enrichment
     │
     ▼
Routing pool automatically updated
```

There must be **no**:

```text
Add provider
→ configure models manually
→ map metadata manually
→ select capabilities
→ configure router manually
```

in the MVP.

## 6.2 A Data-Driven Provider Registry

One of OmniRoute's most successful ideas is a central provider registry. Its own
reference is generated from a central registry file and classifies providers by
auth method (API Key, OAuth, Web Cookie, Local, Search, Audio, Upstream Proxy,
Cloud Agent, and more). VENOM needs the same principle:

```ts
ProviderDefinition {
    id
    name

    authMethods

    capabilities

    discoveryStrategy

    modelsEndpoint

    quotaStrategy

    healthStrategy

    adapter

    metadataResolver
}
```

Adding a new provider should be **plugin-like**.

## 6.3 The Provider Adapter Contract

Every provider adapter must implement a single contract, e.g.:

```text
validateConnection()

discoverModels()

normalizeModelId()

getQuota()

executeChat()

executeStream()

cancelRequest()

translateError()

healthCheck()
```

so the rest of VENOM knows nothing about OpenAI- or Anthropic-specific details.

## 6.4 No Provider-Specific Logic in the Core

Unacceptable:

```ts
if provider === openai ...
if provider === anthropic ...
if provider === kimi ...
if provider === google ...
```

scattered across dozens of files. It must all live inside:

```text
providers/openai
providers/anthropic
providers/google
providers/kimi
```

while the Core only ever sees a `ProviderAdapter`.

## 6.5 The OmniRoute Registry/Connection Pattern

OmniRoute separates the Provider Registry from the Connection; on `auto/*` it
fetches the active connections, verifies credential validity, cross-references
against the Provider Registry, and then builds a candidate pool automatically.
This pattern fits VENOM well, and we extend it to:

```text
Connection Registry
       +
Provider Registry
       +
Model Registry
       +
Runtime Telemetry
       ↓
Routing Candidate Graph
```

## 6.6 Reusing OmniRoute's Provider Registry Data

Instead of writing provider definitions from scratch, we can study/adapt the data
OmniRoute maintains. Its current reference is generated from
`src/shared/constants/providers.ts` — an important point, because it shows the
provider should be a **data-driven definition**, not UI-specific code. The project
can serve as a reference source for:

```text
provider ids
auth types
provider categories
known endpoints
known adapters
model lists
quota behavior
OAuth behavior
```

with a license review before copying any code verbatim.

## 6.7 Adding a New Provider

Developer workflow:

```text
1 ProviderDefinition
1 Adapter
optional MetadataResolver
tests
```

and everything below works automatically:

```text
UI listing
connection
discovery
models
health
routing
venom/pro
venom/max
```

## 6.8 The Custom OpenAI-Compatible Provider Path

§6.7 covers adding a *built-in* provider (a `ProviderDefinition` + adapter in
code). The MVP also needs the complementary **runtime** path: letting the owner add
any OpenAI-compatible endpoint (base URL + key + optional model list) **without
changing code**. This is among the highest-return features in the product — it
turns "we support N providers" into "we support any compatible provider," and it
offsets a decision to ship a deliberately small built-in set (Part XIII). A custom
provider flows through the same adapter contract, discovery, registry resolution,
health, and routing as a built-in one; only its definition is data the owner
supplies at runtime rather than code.

---

# Part VII — Discovery & Background Synchronization

## 7.1 Do Not Re-Discover on Every Request

This point is critical. OmniRoute can build a virtual auto-combo per request from
connections already in the DB and registry, rather than calling every provider to
re-discover models. VENOM should do the same. The request hot path should be
roughly:

```text
Request
   ↓
Model alias
   ↓
Routing policy
   ↓
read in-memory candidates
   ↓
score
   ↓
dispatch
```

and must **not** contain:

```text
fetch models
fetch context size
fetch provider metadata
refresh quota synchronously
```

## 7.2 Async, Parallel Discovery

When discovery is genuinely needed — wrong:

```text
OpenAI
 wait
Anthropic
 wait
Google
 wait
Kimi
 wait
```

right:

```text
      ┌── OpenAI
      ├── Anthropic
START ├── Google
      ├── Kimi
      ├── GLM
      └── MiniMax
```

with:

```text
Concurrency limit
Timeout
Retry budget
Circuit breaker
```

## 7.3 Discovery Budgets

Every discovery job must have:

```text
timeout
max attempts
backoff
max concurrency
```

for example:

```text
Provider timeout: 3–5 sec
Retry: max 1–2
Backoff: async
No blocking UI
```

A bad provider must not slow the whole application.

## 7.4 Negative Caching

If an endpoint does not support:

```text
pricing
context
models
quota
```

do not re-discover that on every attempt. Store:

```text
provider capability:
supports_model_discovery = false
```

or:

```text
metadata endpoint unavailable until:
...
```

This prevents repeated failing requests.

## 7.5 Per-Type TTLs

Not everything needs refreshing at the same rate:

```text
Provider connection state
→ seconds/minutes

Quota
→ minutes or after request

Model availability
→ tens of minutes / hours

Model static metadata
→ days

Context window
→ days/weeks unless version changes

Pricing
→ longer TTL + scheduled refresh

Model health
→ real-time rolling state
```

This alone reduces request volume dramatically.

## 7.6 A Versioned Registry

The Model Registry must be versioned:

```text
registryVersion: 2026.08.08.1
```

and when an update is available:

```text
download delta
       ↓
validate
       ↓
atomic replace
       ↓
reconcile local models
```

without waiting for startup.

## 7.7 Request Deduplication (Singleflight)

If 20 components need the same metadata, do not send 20 requests:

```text
20 requests:
metadata("gpt-x")

        ↓

SingleFlight

        ↓

1 provider/registry operation

        ↓

20 consumers receive same result
```

Very important to avoid slowness at startup or refresh.

## 7.8 Background Workers

We need separate jobs:

```text
ProviderDiscoveryWorker
MetadataReconciliationWorker
QuotaWorker
HealthWorker
RegistryUpdateWorker
MetricsAggregationWorker
```

but **not** microservices. In the MVP these can run inside the same
desktop/backend process.

## 7.9 Registry Update Automation

Later we can add a CI job:

```text
fetch known provider catalogs
compare registry
detect additions
generate change report
run tests
publish registry update
```

but the client does not wait for it — a new model appears automatically as
**Unverified** until a registry update arrives.

---

# Part VIII — Runtime Intelligence & Resilience

## 8.1 The In-Memory Routing Graph

After startup, load a small snapshot into memory:

```text
ProviderConnection
      ↓
Available Models
      ↓
Capabilities
      ↓
Current Health
      ↓
Quota
```

for example:

```text
routingGraph
├── anthropic-account-1
│   ├── claude-opus
│   └── claude-sonnet
│
├── openai-account-1
│   ├── gpt-x
│   └── codex-x
│
└── kimi-account-1
    ├── kimi-x
    └── kimi-y
```

The Router reads from it directly.

## 8.2 Event-Driven Updates

Instead of rebuilding everything:

```text
Provider Connected
→ connection.added event

Discovery Complete
→ models.updated

Quota Changed
→ quota.updated

Request Failed
→ health.updated

Provider Recovered
→ provider.recovered
```

and only the changed part of the routing graph is updated.

## 8.3 Runtime Health Telemetry

A model being theoretically available is not enough. We need runtime telemetry:

```text
success rate
error rate
429 rate
p50 latency
p95 latency
TTFT
stream failure
timeout rate
recent failures
```

per:

```text
Provider
Connection
Model
```

## 8.4 Circuit Breakers

OmniRoute uses circuit-breaker state within health scoring, temporarily excluding
bad routes with recovery/probe behavior. VENOM needs this too:

```text
5 recent failures
        ↓
OPEN
        ↓
no normal traffic
        ↓
cooldown
        ↓
HALF OPEN
        ↓
probe
        ↓
success
        ↓
CLOSED
```

## 8.5 Per-Connection Health

A provider must not disappear entirely because of one account. State must be
**per connection**:

```text
Claude account A
quota exhausted

Claude account B
healthy
```

result:

```text
Anthropic still available
```

## 8.6 Quota-Aware Routing

Quota is not just a UI value; it must feed routing:

```text
Account A:
remaining = 5%

Account B:
remaining = 75%
```

The Router prefers B. OmniRoute already makes quota/headroom a factor in
auto-combo scoring.

## 8.7 Last-Known-Good Path

OmniRoute's Last-Known-Good Path (LKGP) idea is very important. If, for example,
`Claude Account 2` just succeeded for the same kind of traffic and its health was
good, there is no need to change the route unnecessarily. We use a
**Last-Known-Good Provider/Model** with a short expiry. OmniRoute uses this idea
to maintain session stickiness in `auto`.

## 8.8 Retry Policy

We do not want:

```text
Request fails
→ retry same broken provider 5 times
```

but rather:

```text
attempt A
  ↓ failure
classify error
  ↓
retryable?
 /      \
no       yes
         ↓
 alternate candidate
```

with a global retry budget.

## 8.9 Error Normalization

Every provider has its own errors. We normalize them to:

```text
AUTH_ERROR
QUOTA_EXHAUSTED
RATE_LIMITED
MODEL_NOT_AVAILABLE
CONTEXT_OVERFLOW
PROVIDER_DOWN
TIMEOUT
INVALID_REQUEST
CONTENT_POLICY
UNKNOWN_PROVIDER_ERROR
```

so the Router can act accordingly.

## 8.10 The Fallback Graph

Instead of a simple fallback list `A → B → C`, use:

```text
Primary Candidate Pool
       ↓
alternate healthy candidate
       ↓
alternate provider
       ↓
reduced tier
```

while preserving capabilities: when a vision model fails, we must not fall back to
a text-only model.

## 8.11 Self-Healing

OmniRoute applies temporary exclusions, cooldown recovery, probe requests, and
incident behavior when many providers become unhealthy. These are excellent ideas
for VENOM — the system should repair routing automatically without user
intervention.

---

# Part IX — Routing & Virtual Models

> **Naming note.** In the current project, the "virtual models" below are the three
> product **tiers** exposed to inference clients — `venom/lite`, `venom/pro`, and
> `venom/max` — sent as `"model": "venom/max"` on the OpenAI-compatible endpoint.
> `venom/lite` is free-accounts-only (fast, zero-cost), `venom/pro` adds paid
> offerings and extended thinking, and `venom/max` targets maximum quality with
> the largest context and thinking budget. The authoritative tier policy lives in
> the tier engine specification.

## 9.1 Virtual Models

The external interface exposes the three tiers:

```text
venom/lite
venom/pro
venom/max
```

while the real underlying models remain a hidden implementation detail. This is
close to Zero-Config Auto Routing in OmniRoute, where a client can send `auto`,
`auto/coding`, `auto/fast`, `auto/cheap`, etc., while a virtual candidate pool is
built internally. VENOM takes the concept further.

## 9.2 `venom/pro`

Goal:

```text
High quality
Fast
Reliable
Cost conscious
```

Candidate selection depends on:

```text
task fit
availability
latency
health
context
cost
quota
```

## 9.3 `venom/max`

Goal:

```text
Maximum result quality
```

Pipeline:

```text
Request
   ↓
Task Classification
   ↓
Complexity Estimation
   ↓
Capability Requirements
   ↓
Context Requirements
   ↓
Candidate Filtering
   ↓
Quality Ranking
   ↓
Runtime Health Ranking
   ↓
Dispatch
```

and, for complex tasks in the future:

```text
primary model
      ↓
confidence / verifier
      ↓
optional second model
```

But we do not implement heavy multi-model orchestration in the first MVP.

## 9.4 Multi-Factor Scoring

OmniRoute uses multi-factor scoring; its current factors include health, quota,
cost, latency, task fitness, model specificity, stability, and context
suitability, plus Last-Known-Good behavior. VENOM should build a similar scoring
engine:

```text
score =
    taskFit
  + quality
  + health
  + contextFit
  + availability
  + quota
  + latency
  + costEfficiency
  + stability
```

but with weights that differ per tier.

## 9.5 Filter Before You Score

More important than scoring itself. If a request needs:

```text
150K context
vision
tools
```

first remove every model that does not meet the hard requirements:

```text
100 models
   ↓
Capability filter
   ↓
22
   ↓
Context filter
   ↓
11
   ↓
Account availability
   ↓
8
   ↓
Health filter
   ↓
6
   ↓
Scoring
   ↓
best model
```

This is faster and more accurate.

## 9.6 Dynamic Candidate Pools

One of OmniRoute's best ideas is that `auto/*` needs no fixed combo in the DB; a
virtual factory builds the candidate pool from active provider connections,
verifies credentials, then links them to the provider registry. We use the same
concept: `venom/max` has no manually saved model list. Instead:

```text
current active connections
        +
available models
        +
capabilities
        +
policy
        =
dynamic candidate pool
```

So:

```text
Connect new provider
```

automatically means:

```text
venom/pro and venom/max gain new candidates
```

with no manual edits.

## 9.7 The Routing Hot Path

Should be:

```text
Request
   │
   ▼
Resolve virtual model (tier)
   │
   ▼
Infer requirements
   │
   ▼
Read routing graph
   │
   ▼
Filter candidates
   │
   ▼
Score
   │
   ▼
Dispatch
```

All data is already in memory.

## 9.8 `venom/pro` Policy Example

```text
Task fit       25%
Quality        20%
Health         15%
Latency        15%
Context fit    10%
Quota          7%
Cost           5%
Stability      3%
```

Goal: a `strong daily model`.

*(Illustrative weights only; the authoritative, validated per-tier weights are
owned by the tier engine.)*

## 9.9 `venom/max` Policy Example

```text
Task fit       30%
Quality        30%
Context fit    15%
Health         10%
Stability       5%
Quota           4%
Latency         3%
Cost            3%
```

Quality first.

*(Illustrative weights only; the authoritative, validated per-tier weights are
owned by the tier engine.)*

## 9.10 Hard Constraints

Some things are not scores; they are conditions:

```text
required context <= model context
required input type supported
tools available if required
connection authenticated
circuit breaker not open
model currently available
```

If unmet, the candidate is removed.

## 9.11 Task Intelligence

In the MVP we do not need an expensive LLM classifier per request. We start with a
hybrid classifier:

```text
request features
+
tool usage
+
file extensions
+
prompt patterns
+
context size
+
client type
```

which determines:

```text
coding
debugging
architecture
analysis
writing
simple
long-context
agentic
```

almost for free. A small semantic classifier can be added in a later version.

## 9.12 A Local Classifier, Not an LLM Router

If we need another model just to choose a model, we have added:

```text
latency
cost
failure point
```

before the actual request. Use a local classifier first; reserve LLM
classification for ambiguous cases in the future.

---

# Part X — Data Model

## 10.1 Overview of Tables

The core tables:

```text
providers
provider_connections
canonical_models
provider_models
connection_models
model_aliases
model_capabilities
model_metadata_sources
runtime_health
runtime_metrics
quota_snapshots
virtual_models
routing_policies
discovery_runs
```

## 10.2 `providers`

Static provider definitions.

```text
openai
anthropic
google
kimi
zai
...
```

## 10.3 `provider_connections`

User accounts.

```text
id
provider_id
auth_type
status
last_validated
default_model
credential_reference
```

We do **not** store credentials in plaintext.

## 10.4 `canonical_models`

Unified models.

```text
id
canonical_id
vendor
family
display_name
status
```

## 10.5 `provider_models`

Mappings.

```text
provider_id
provider_model_id
canonical_model_id
```

## 10.6 `connection_models`

Availability.

```text
connection_id
canonical_model_id
provider_model_id
available
last_seen
```

## 10.7 `model_capabilities`

```text
model_id
capability
value
confidence
source
verified_at
```

## 10.8 `runtime_metrics`

We do not need to store every request forever in the MVP. We keep rolling
metrics:

```text
requests
success
failures
p50
p95
ttft
rate_limits
```

---

# Part XI — Deployment Shape

## 11.1 A Modular Monolith, Not Microservices

To accelerate the MVP, use a **modular monolith**:

```text
VENOM ROUTER
├── providers
├── discovery
├── registry
├── models
├── routing
├── telemetry
├── virtual-models
└── api
```

One process. One database. Internal events. In-memory cache. That is sufficient.

## 11.2 SQLite for the MVP

If the app is desktop/local, SQLite is very suitable for:

```text
providers
connections
model metadata
snapshots
settings
routing history
```

There is no reason to build Redis/Postgres infrastructure for the MVP unless it
already exists for another reason.

## 11.3 MVP Delivery Architecture

```text
┌─────────────────────────────┐
│        VENOM UI             │
└──────────────┬──────────────┘
               │
┌──────────────▼──────────────┐
│        Venom API            │
├─────────────────────────────┤
│ Virtual Model Service       │
│ Routing Service             │
│ Model Service               │
│ Provider Service            │
├─────────────────────────────┤
│ Model Registry              │
│ Provider Registry           │
│ Discovery Engine            │
│ Metadata Resolver           │
│ Health Engine               │
├─────────────────────────────┤
│ Memory Routing Graph        │
│ SQLite / Local Store        │
├─────────────────────────────┤
│ Provider Adapters           │
└──────────────┬──────────────┘
               │
       External Providers
```

## 11.4 Backup & Restore

A local single-file product still needs a real backup story. Ship a **single
sealed container** — authenticated encryption (AEAD) with a key derived from a
passphrase (e.g. Argon2id), a **consistent** SQLite snapshot, a wrapped data key,
and version/integrity metadata — that round-trips losslessly. **Never** back up or
ship the raw `venom.db` plus the OS keyring as a loose pair: the database is
useless without the key material, and shipping them together leaks it.

---

# Part XII — Testing & Observability

## 12.1 Provider Contract Test Suite

A contract test suite must be created. Any new provider must pass:

```text
authentication
discovery
normalization
chat
stream
error mapping
timeout
model unavailable
rate limit
credential failure
```

## 12.2 Registry Tests

Tests for:

```text
alias → canonical
provider ID → canonical
unknown version → family
duplicate IDs
conflicting metadata
missing context
metadata confidence
```

## 12.3 Performance Tests

Test targets:

```text
500 models
20 connections
multiple providers
```

and `/models` must cause **no** network requests, while routing lookup stays fast.

## 12.4 Observability Dashboard

A simple internal dashboard:

```text
Providers
Connections
Models discovered
Canonical matched %
Unverified models
Discovery latency
Metadata cache hit %
Routing latency
Provider health
Fallback rate
```

The most important metric during the fix phase is:

```text
canonicalization_rate
```

targeting `>99%`.

**Secret hygiene is part of observability, not separate from it.** Route-decision
records, per-attempt logs, any sanitized response headers (e.g. `X-Venom-*`), and
every diagnostic trace must **never** expose provider names, account IDs, raw
provider errors, or credentials — only sanitized, provider-neutral facts. And a
failure to record consumption must surface as an error, never be silently
swallowed: an unlogged request is an unaccounted one.

## 12.5 The "Unresolved Models" Metric

Instead of counting `UNKNOWN`, track:

```text
unresolved_models_total
```

which should reach `≈0` for officially supported providers. Each unresolved model
records:

```text
provider
raw ID
resolver stage
failure reason
```

so we fix the resolver, not the UI.

## 12.6 Metadata Coverage Score

Per model:

```text
Metadata coverage = 87%
```

for example:

```text
identity        ✓
family          ✓
context         ✓
tools           ✓
vision          ✓
pricing         ?
max output      ?
```

Far better than the word `UNKNOWN`.

## 12.7 Enforce Principles as Machine Gates

The principles in this report must be **machine-enforced, not trusted**. Two gates
matter most:

- **Zero-hardcoding lint.** The registry-driven thesis (no `UNKNOWN`, no hardcoded
  truth) is only real if a build gate forbids any hardcoded model name, context
  window, capability flag, or price in production code — outside tests and
  fixtures. Without the gate, hardcoded values creep back in silently.
- **Sustained-load gate.** Beyond the "no network on `/models`" test (§12.3), run a
  sustained load — e.g. **≥30 minutes at ≥20 RPS / ≥20 concurrent requests**
  against mock backends — asserting **≤0.5% internal error rate**, a reported **p95
  routing latency**, and **zero invariant violations**: no quota overrun on any
  window, the free-only tier (`venom/lite`) never selects a paid offering, and no
  secret appears in any log.

## 12.8 Live Evidence Over Written Procedure

A written runbook is a plan, not proof. Readiness requires **dated evidence of a
real live run**: a real account onboarded end-to-end (identity, funding, health),
and a real SDK routed through `venom/lite | pro | max` for chat, streaming, tools,
and vision. Beware **apparent-maturity bias** — a large test suite and strict docs
can imply a readiness that unmeasured live behavior has not confirmed. The gap
between what tests assert and what a real provider does is only closed by running
it and recording the result, including what failed.

---

# Part XIII — MVP Scope, Phasing & Roadmap

## 13.1 What Not to Do

- Do **not** run internet scraping on every startup.
- Do **not** call providers for the context window on every UI open.
- Do **not** treat `/models` as a metadata database.
- Do **not** use `UNKNOWN` when the model ID is known.
- Do **not** make the user choose capabilities manually.
- Do **not** make the user build combos manually for the tier models.
- Do **not** add provider-specific conditionals to the routing core.
- Do **not** refresh all metadata with the same TTL.
- Do **not** probe every model on every startup.
- Do **not** wait for discovery before making the app usable.

## 13.2 MVP Scope — P0

The MVP does not need everything OmniRoute has; only what directly hits the
current problems.

```text
Provider Registry
Provider Adapter Contract
Canonical Model Registry
Model Alias Resolution
Async Model Discovery
Local Cache
UNKNOWN elimination
Connection/Model mapping
Fast startup
```

## 13.3 MVP Scope — P1

```text
Runtime health
Quota awareness
Fallback
Circuit breaker
Dynamic candidate pools
venom/pro
venom/max basic routing
```

## 13.4 MVP Scope — P2

After launch:

```text
Task classifier
Advanced semantic routing
Capability probing
Quality learning
Model exploration
Verifier models
Multi-model collaboration
```

## 13.5 First Providers for the MVP

Pick a small set of providers important to the current project. What matters is a
**stable provider contract**, so that adding the 20th or 200th provider later
requires no Core changes.

## 13.6 The "Fully Automatic" Goal

The end goal: the user only does `Connect Account`, and the system automatically:

```text
✓ validate
✓ discover
✓ normalize
✓ match models
✓ enrich metadata
✓ detect capabilities
✓ monitor quota
✓ monitor health
✓ add candidates
✓ route requests
✓ fallback
✓ recover
```

without a long wizard.

## 13.7 Immediate Fix: Eliminating UNKNOWN

Proposed implementation:

1. Find every place that produces `UNKNOWN` / `Unknown` / `unknown` in the
   model/domain layer.
2. Determine the cause: model ID absent? mapping absent? metadata absent?
   normalization failed? provider adapter missing?
3. Replace the current fallback with a `DetectedModel` that preserves the raw
   model ID.
4. Apply the canonical resolver.
5. Add the alias map.
6. Add the family resolver.
7. Add confidence metadata.
8. The unknown model enters the DB automatically.

## 13.8 Immediate Fix: Eliminating Startup/UI Latency

Stop any synchronous metadata fetch from:

```text
app startup
models page load
provider list page
request routing
```

and move it to discovery/enrichment workers.

## 13.9 Implementation Phases

**Phase 1 — Stabilize.** The first goal is not the tiers; it is making the
provider/model layer correct and fast:

```text
remove blocking discovery
separate connections/providers/models
introduce registry
introduce canonical IDs
introduce local snapshots
fix UNKNOWN
```

**Phase 2 — Automate.**

```text
auto discovery
background reconciliation
automatic provider→model mapping
quota updates
health updates
```

Result: `Connect once → ready automatically`.

**Phase 3 — Resilience.** Port OmniRoute's ideas:

```text
health scoring
circuit breaker
fallback
LKGP
quota-aware routing
```

OmniRoute already shows how health, quota, latency, cost, task fit, and more feed
a dynamic auto-combo with self-excluding damaged routes.

**Phase 4 — Venom Tiers.** Once the data is trustworthy, launch `venom/pro` and
`venom/max`. Smart routing over bad metadata yields bad decisions, so ordering
matters:

```text
Data correctness
      ↓
Availability
      ↓
Runtime telemetry
      ↓
Routing
      ↓
Intelligence
```

**Phase 5 — Learning Router.** After the MVP, the router can learn from outcomes:

```text
Task:
large React refactor

Models historically:

Model A:
92% successful
11s average

Model B:
78%
6s

Model C:
95%
22s
```

`venom/pro` may pick A; `venom/max` may pick C.

## 13.10 Recommended MVP Order

```text
P0
│
├── Provider Registry
├── Adapter Contract
├── Model Registry
├── Canonical IDs
├── Aliases
├── UNKNOWN Resolver
├── Cache-first ModelService
└── Async Discovery
     │
     ▼
P1
│
├── Connection Availability
├── Runtime Graph
├── Quota
├── Health
├── Error Normalization
├── Circuit Breaker
└── Fallback
     │
     ▼
P2
│
├── venom/pro
├── Dynamic Candidate Pool
└── Weighted Router
     │
     ▼
MVP RELEASE
     │
     ▼
P3
│
├── venom/max
├── Task Intelligence
├── Better quality scores
├── Model learning
└── Verifier / multi-model features
```

This path reduces risk, solves the problems currently blocking the project, and
avoids wasting MVP time rebuilding infrastructure whose ideas mature projects like
OmniRoute have largely solved.

## 13.11 Target Onboarding Experience

The user:

```text
Connect Kimi
```

VENOM:

```text
✓ Authenticated
✓ Account registered
✓ Models discovered
✓ Models canonicalized
✓ Metadata attached
✓ Quota detected
✓ Health initialized
✓ Candidate graph updated
✓ venom/pro updated
✓ venom/max updated
```

with no extra configuration.

## 13.12 Before & After

**Before**

```text
Start app
   ↓
Fetch providers
   ↓
Fetch models
   ↓
Fetch metadata
   ↓
Wait
   ↓
Some models UNKNOWN
   ↓
UI incomplete
```

**After**

```text
Start app
   ↓
Load Registry + Snapshot
   ↓
READY
   ↓
UI immediately populated
   ↓
background reconcile
   ↓
silent incremental updates
```

## 13.13 Key MVP KPIs

```text
Startup Blocking Network Calls
Target: 0

Models UI Blocking Network Calls
Target: 0

Routing Metadata Network Calls
Target: 0

Known Model Resolution
Target: >99%

Provider Connection Automation
Target: 100%

Model Discovery Automation
Target: 100%

UNKNOWN Display Names
Target: 0

Routing decision latency
Target: extremely low / local only

Metadata cache hit
Target: >95%
```

---

# Part XIV — Conclusion & Guiding Principles

## 14.1 Final Recommendation

We recommend against continuing to fix the current problem by:

```text
add another metadata endpoint
add another fallback
add another special case
add another UNKNOWN mapping
```

because this makes the system more complex with every provider. The root-cause fix
is:

1. Create a **Canonical Model Registry**.
2. Separate **Discovery from Metadata**.
3. Move to **Cache-First / Async Refresh**.
4. Keep all metadata/network discovery out of the hot path.
5. Redesign provider integrations around an **Adapter Contract**.
6. Build a **data-driven Provider Registry** inspired by OmniRoute.
7. Turn `UNKNOWN` into **Detected + Metadata Confidence**.
8. Apply an Alias + Family + Canonical resolution pipeline.
9. Add a dynamic runtime model graph.
10. Port health / quota / circuit-breaker / LKGP / fallback concepts from
    OmniRoute.
11. After this layer stabilizes, launch `venom/pro` and `venom/max` as
    VENOM-owned tiers.

## 14.2 The Final Architecture Principle

VENOM ROUTER should not ask, on every run:

> What do the providers tell me right now about every model?

It should already know:

> What do I know about this model?

and then ask the provider only:

> Is this model currently available through this connection?

This is the dividing line between the current slow design and the desired one.

## 14.3 Recommended Final Architecture

```text
                     ┌────────────────────┐
                     │    venom/max       │
                     │    venom/pro       │
                     │    venom/lite      │
                     └─────────┬──────────┘
                               │
                     ┌─────────▼──────────┐
                     │ Venom Intelligence │
                     │      Router        │
                     └─────────┬──────────┘
                               │
                 requirements / task / policy
                               │
                     ┌─────────▼──────────┐
                     │ Candidate Engine   │
                     └─────────┬──────────┘
                               │
          ┌────────────────────┼────────────────────┐
          │                    │                    │
          ▼                    ▼                    ▼
    Model Registry       Runtime Health       Connections
          │                    │                    │
          └────────────────────┼────────────────────┘
                               │
                     Effective Model Graph
                               │
                     ┌─────────▼──────────┐
                     │ Provider Adapters  │
                     └─────────┬──────────┘
                               │
       ┌───────────────┬───────┼─────────┬──────────────┐
       ▼               ▼       ▼         ▼              ▼
    OpenAI         Anthropic  Kimi      GLM           Gemini
```

## 14.4 Strategic Summary

The VENOM ROUTER MVP should not compete with OmniRoute on:

> Who can support the most providers?

That battle would consume all development time. VENOM should leverage the
experience embodied in OmniRoute to solve the layer of:

```text
Providers
Models
Accounts
Discovery
Health
Quota
Fallback
```

and then compete at the higher layer:

```text
                 VENOM

What is the user's task?

Which model is actually best for it?

Which available account should execute it now?

Should we prioritize quality, speed or cost?

How can venom/pro feel like one coherent model?

How can venom/max outperform choosing a provider manually?
```

This is where VENOM ROUTER's real value lives.

> **OmniRoute should solve infrastructure knowledge.**
>
> **VENOM should own model intelligence.**

## 14.5 The Final Architectural Rule

> **Provider discovery tells VENOM what is available.**
>
> **The Venom Registry tells VENOM what the model is.**
>
> **Runtime telemetry tells VENOM how the model is behaving now.**
>
> **Venom Intelligence decides whether that model should execute the task.**

When these four boundaries are clear, most of the current `UNKNOWN`, latency,
duplication, and complexity in the model layer disappears — and building
`venom/pro` and `venom/max` becomes a natural extension of the system rather than
a fragile new layer bolted on top.

The single most important point of this report is **not to merely optimize the
existing fetch**. The problem is deeper: fetching metadata from providers must not
be a core part of the model-display path or the router's execution path at all.
Once VENOM becomes **Registry + Discovery + Runtime State**, the problems of
speed, `UNKNOWN`, and automatic onboarding change fundamentally.

---

# Part XV — Production-Readiness Decision

> The chapters above (Parts I–XIV) describe the *target architecture*. The chapters
> below record a separate, grounded decision made on **2026-08-08** against the
> actual state of the code: which of two existing builds should carry the MVP, and
> exactly what still stands between it and a shippable v1.0.

Two builds were examined:

- **The Go desktop build** — this repository (`Desktop\venom-router`) —
  Go 1.26 / SQLite / a single binary.
- **The TypeScript build** — `F:\projects\venom-router` — TanStack Start + React 19
  / Bun / SQLite (after a migration off Supabase).

## 15.1 The Decision

> ### Chosen build: **the Go desktop build.**

It is the closest to a shippable MVP, the most efficient and performant, and the
strongest in engineering guarantees. The decision is **not** a clean sweep on every
axis — the TypeScript build leads on one important axis (provider breadth and actual
live operation), stated honestly in §15.4.

## 15.2 Comparison by the Numbers

| Axis | Desktop (Go) | TypeScript (F) |
|---|---|---|
| Production code | 50,709 lines Go | 71,339 lines TS/TSX |
| Test code | **76,560 lines** (1.5:1 ratio) | 172 test files (~310 tests) |
| Test functions | **1,795** | ~310 |
| CI | ✅ `ci.yml` + `race.yml`, self-hosted runner, version-pinned toolchain | ❌ **`.github/` deleted entirely** |
| Git state | clean (one untracked file) | **30+ modified, uncommitted files** |
| Final artifact | `dist/venom.exe` — **29 MB single binary**, built 2026-08-07 | `dist/client` + `dist/server`, needs Node/Bun |
| Runtime dependencies | **zero** | Node ≥22 + Bun + `node:sqlite` (experimental) |
| Database | in-process SQLite, 17 migrations | SQLite via a Supabase-emulation shim, 40 tables |
| Design system | standalone `@venom/design-system` + drift gates + visual regression (win32 + linux) | shadcn/ui directly in-app |
| Providers (fully registered) | 5 of 9 | ~14 of 23 |
| Repo age | 469 commits in 16 days (23 Jul → 7 Aug) | ~6 weeks (22 Jun → 8 Aug) |
| Proven live operation | ❌ runbooks written, not executed | ✅ **real API & health logs**, 5 Jul → 4 Aug |

## 15.3 Why the Desktop Build

**Efficiency.** The desktop build ships as a **single executable** — double-click
and the server starts with the tray icon. No Node, no Bun, no `bun install`, no
external service, no mandatory environment variables beyond what first run
generates. The TypeScript build needs a live Node/Bun environment, depends on
`node:sqlite` (still experimental in Node 22), and still has two call sites hitting
the real Supabase cloud client (`src/server.ts:395` and
`src/lib/api/handlers/workers.ts:31`) — so running it without `SUPABASE_URL` fails
on at least two paths.

**Performance.** Compiled Go versus Node SSR — a clear difference in response time
and memory footprint under load. In-process SQLite through typed repositories versus
a shim emulating Supabase's `.from().select().eq()` chain over `node:sqlite` — the
shim adds cost and error surface with no justification once the migration is
complete. The desktop build has `test-race` (CGO race detector),
`internal/execution/inflight.go`, a per-provider concurrency limit, and a
multi-window atomic quota-reservation model; the TypeScript build's own audit report
flagged an **actual race gap** in its API-key bounds.

**Closest to production.** This is the contested axis, and it deserves detail. In
the desktop build's favor:

- One documented gate (`task gate`): gofmt, goimports, go vet, golangci-lint,
  import-layer tests, a ban on `switch` over provider names, and a **secret canary**
  proving no injected secret appears in any output.
- CI verifies the toolchain version byte-for-byte and blocks fork PRs from running
  on the self-hosted runner.
- Verification that the built exe is genuinely GUI-subsystem by reading the PE field
  — added after a real incident on 6 Aug.
- Visual baselines for both win32 and linux.
- A complete, tested routing engine: hard gates → route groups → weighted scoring →
  competitive band → Pro's funding-mix deficit controller → Max's quota-fair
  DRR + P2C → fair account selection, with scoped circuit breakers and classified
  fallback.
- A complete public API: `/v1/chat/completions` + `/v1/models`, SSE streaming, the
  `venom` extension, `X-Venom-*` headers, and consumption logging on every terminal
  route.

## 15.4 The Honest Counterpoint & the Decisive Point

In the TypeScript build's favor:

- **It has actually run.** A `LOGS/` folder holds real API and health logs over a
  month. The desktop build has three live-verification runbooks (`P2b-TEST-003`,
  `P5-TEST-001`, `P6-TEST-002`) — but they are **written procedures, not recorded
  results**. There is no dated proof that a single real account passed through the
  desktop build.
- Provider breadth is wider: `codex`, `github-copilot`, and `xai` exist in the
  TypeScript build and are absent from the desktop build.
- Its dashboard surface is broader: 24 routes, including a playground, benchmark,
  script console, token-health, and tier-status.

**The honest bottom line:** the TypeScript build is closer to *"works right now on
the owner's machine."* The desktop build is closer to *"can be released as a
product."* Since an MVP is a **release**, not a personal deployment, the desktop
build wins.

**The decisive point:** the TypeScript build is architecturally migrating toward
what the desktop build **was born as** — local SQLite, local owner auth, one
process. Its migration is ~95% done but still carries migration debt (the shim, two
remaining Supabase call sites, hand-patched generated types). Investing in it means
finishing a migration just to reach where the desktop build already stands.

---

# Part XVI — MVP Gap Analysis

Measured against the actual state of the code, nine real gaps remain, ordered by
severity.

## 16.1 Critical Gaps (Release-Blocking)

| # | Gap | Evidence |
|---|---|---|
| **B1** | **Open handoff: ClinePass + Live Models incomplete**, explicitly rejected by the owner on visual grounds | `docs/handoffs/2026-08-04-...-handoff.md`: "incomplete and not visually acceptable; must not be presented as finished" |
| **B2** | **No single piece of live-run evidence.** The three runbooks are unexecuted procedures | `docs/evidence/` holds 3 runbooks + 0 dated results |
| **B3** | **Two CI gates are empty** — `schema-lint` and `no-hardcoding-lint` are marked `RESERVED placeholder`, although the project's principle #1 is "zero hardcoding" | `Taskfile.yml:196` and `:205` |
| **B4** | **The Bifrost transport is still a test shim** — no streaming, no cancellation | `internal/execution/bifrost.go:222,233` |

## 16.2 Important Gaps (Weaken the Release)

| # | Gap | Evidence |
|---|---|---|
| **B5** | **4 of 9 providers have registration seams only** (`seams`) without a full lifecycle (`usability`): antigravity, agnes_ai, nvidia_nim, ollama_cloud | `internal/httpapi/*_seams.go` with no matching `*_usability.go` |
| **B6** | **No "Custom OpenAI-Compatible" path** — a new provider cannot be added without code changes | no custom registration in `internal/providers/` |
| **B7** | **No backup / restore** — Phase 8 not started | no backup package in `internal/` |
| **B8** | **The sustained-load gate has not been run** (≥30 min, ≥20 RPS, ≤0.5% internal errors) | Phase 8 |
| **B9** | **The race detector is non-blocking** — nightly schedule only | `.github/workflows/race.yml` |

---

# Part XVII — Execution Plan to v1.0

Five phases, each ending in a **provable gate**; no phase starts before its
predecessor's gate is green.

> **This remediation phasing is distinct from — and runs alongside — the
> *architecture* phasing in Part XIII.** Part XIII builds out the target
> architecture (registry → automation → resilience → tiers); the phases here close
> the concrete readiness gaps of the current build. They are complementary, not
> competing.

## 17.1 M1 — Close Open Debt

**Goal:** no announced-but-incomplete work remains in the repo.

1. Read the handoff file in full
   (`2026-08-04-codex-incomplete-clinepass-live-models-handoff.md`) and treat it as
   a contract, not a suggestion.
2. Audit the mixed worktree — the file itself warns it contains uncommitted edits
   from two agents. Inspect every edit: fix it or delete it. Keep nothing merely
   because it exists.
3. Fix ClinePass through the full cycle: register → discover → certify → route a
   real request, referring to `docs/evidence/clinepass-legacy-wire-reference.md`.
4. Live Models cycle: prove the refresh is real — automatic requests + stored data
   that changes, not just a "X minutes ago" label moving.
5. Visual fix on port 8088 (not 8081), in both themes, in a wide and a narrow
   window, honoring `Design_System` — and if the flaw is in the system itself, fix
   it in the shared source and test the consumers.

**Gate M1:** the handoff is closed with a counter-report naming every item and how
it was proven. `task gate` green. Git tree clean and committed.

## 17.2 M2 — Activate the Empty Gates

**Goal:** non-negotiable principles are enforced automatically, not trusted.

1. `no-hardcoding-lint` — write the real checker: forbid any hardcoded model name,
   context window, capability flag, or price in `internal/` outside tests and
   fixtures. This is principle #1 and must be blocking.
2. `schema-lint` — write the checker: NULLable numeric columns mean "unknown,"
   append-only evidence tables keep exactly one current row, every table links to
   `account_id`.
3. Add both to `task gate` and remove the word `RESERVED` from the description.
4. Make `race.yml` blocking on `main` instead of nightly-only — the reservation
   model and the janitor are inherently concurrent.

**Gate M2:** `task gate` includes five blocking checks. Running the two checkers on
the current repo yields zero violations (or a fully remediated list).

## 17.3 M3 — Resolve Bifrost

**Goal:** remove a half-built transport from the production path. Take **one**
decision explicitly and record it in `docs/01-architecture.md`:

- **(a) Drop Bifrost from V1** — the native transports (`openaicompat`,
  `nativeapi`, `nativeoauth`, `anthropicwire`, `geminiwire`) already cover every
  actually-shipped provider. Remove `TransportKindBifrost` from the closed
  vocabulary, drop the submodule from the build path, and keep it as a documented
  future option.
- **(b) Complete Bifrost** — implement `Stream` and `Cancel` for real, with a full
  contract test against a mock server.

**Recommendation: (a).** The shim only ever served the Phase-0 connectivity proof
and has done its job. Keeping it in the transport vocabulary means any route
resolving to `bifrost` fails on streaming — a silent production failure.

**Gate M3:** no transport in the tree returns "not implemented." `grep` for
`not implemented` in `internal/` outside tests = zero.

## 17.4 M4 — Dated Live Evidence

**Goal:** turn the runbooks from procedures into proof.

1. Execute `P2b-TEST-003` — connect one real API-key account and one real OAuth
   account. Prove identity, funding, and health in the fleet view. Record the
   result as a dated file in `docs/evidence/`.
2. Execute `P5-TEST-001` — point a real SDK (OpenAI SDK + Claude Code) at
   `http://127.0.0.1:8081/v1` and use `venom/lite | pro | max` for chat, streaming,
   tools, and vision. Prove the `venom` extension clamps thinking budget above the
   tier ceiling and that it survives streaming.
3. Execute `P6-TEST-002` — the human leg: double-click `venom.exe`, silent boot,
   tray, a full cycle without opening a terminal.
4. Add a "Results" section to each runbook with date, screenshots, and numbers — not
   claims.

**Gate M4:** three dated evidence files in `docs/evidence/`, each stating the date,
the account used, and the actual result including what failed.

## 17.5 M5 — Close V1 Scope & Harden

**Goal:** what ships is complete; what does not ship is declared.

1. **Provider scope decision — recommend narrowing.** The MVP does not need nine
   providers. Ship the **five complete ones** (`opencode-zen`, `claude-code`,
   `clinepass`, `gemini-cli`, `openai-generic`) and mark the other four "coming
   soon" in the UI rather than shipping half an integration. If you want one more,
   make it `antigravity`, because its OAuth path is cited as a reference in the
   roadmap.
2. **Custom OpenAI-Compatible path** — implement it (see §6.8). It is the highest
   return per line of code in the whole project: it makes any compatible provider
   addable **without code changes**, offsetting the narrowed built-in list.
3. **Backup and restore** — a single AEAD container, an Argon2id-derived key from a
   passphrase, a consistent SQLite snapshot, a wrapped data key, and version /
   integrity metadata (see §11.4). **Never** ship raw `venom.db` + keyring as a
   pair.
4. **Sustained-load gate** — ≥30 continuous minutes at ≥20 RPS / ≥20 concurrent
   requests against mock backends, an internal error rate ≤0.5%, a p95
   routing-latency report, and zero invariant violations: no quota overrun on any
   window, no paid selection in `venom/lite`, no secret in any log (see §12.7).
5. **Signed build + first-run experience** — sign the exe, and a quick-start path
   (create key → connect provider → point the client → watch requests).

**Gate M5 (the release gate):** a clean machine installs, runs, connects a real
provider, and serves a request from a real SDK — all without opening a terminal.
Backup round-trips with full integrity. The load gate is green.

## 17.6 Recommended Order

| Phase | Deliverable | Blocking |
|---|---|---|
| **M1** | Close the handoff | 🔴 yes |
| **M2** | The two empty CI gates work | 🔴 yes |
| **M3** | Resolve Bifrost (drop recommended) | 🔴 yes |
| **M4** | Three dated live-run evidence files | 🔴 yes |
| **M5** | Narrow providers + custom path + backup + load + signing | 🟠 release gate |

**The critical path to MVP is M1 → M4.** M2 and M3 are independent and can run in
parallel with M1.

---

# Part XVIII — Migration Assets & Final Warnings

## 18.1 What to Extract from the F Build Before Freezing

The TypeScript build is not a loss — it holds assets worth porting:

1. **Wire references for the three missing providers**: `codex.server.ts` +
   `codex-quota.server.ts` + `codex-model-discovery.server.ts`,
   `github-copilot.server.ts`, and the `xai-*.server.ts` family. This is real,
   run-proven protocol knowledge — port it as wire references into `docs/evidence/`
   before freezing.
2. **`PROVIDER-INVENTORY.md` (96 KB) and `PROVIDERS.md` (37 KB)** — a large provider
   inventory; merge it into `docs/03-provider-integration-catalog.md`.
3. **The `LOGS/` records** — real behavior of real providers over a month. Use them
   to build realistic fixtures for the desktop build's contract tests.
4. **Dashboard-surface ideas** with no desktop equivalent: the `script` console,
   `token-health`, and `tier-status`.
5. **`AUDIT_REPORT.md`** — its ten-bug list is an excellent checklist for a security
   review of the desktop build too (especially: leaking provider and account names
   in routing traces, and swallowing consumption-logging failures).

## 18.2 Rotate the F Build's Secrets Now

Before freezing: the TypeScript build contains a `.env` with the owner's password in
cleartext, a Supabase `service_role` key, and an `ANTIGRAVITY_CLIENT_SECRET`.
**Rotate all of these now** — the audit report notes that an OAuth secret was
hardcoded for a period, meaning it may live in Git history.

## 18.3 Two Final Warnings

1. **Apparent-maturity bias.** The desktop build is only 16 days old. Its test
   volume (1,795 functions) and documentation rigor give an impression of maturity
   that may run ahead of reality, because **zero proven live operation** means the
   gap between what the tests assert and what happens with a real provider has not
   yet been measured. Phase M4 exists precisely to measure that gap; it must not be
   skipped or replaced with a claim.
2. **Do not run both builds against the same accounts at the same time.** Both
   reserve quota and record consumption independently. Running them in parallel
   corrupts quota accounting in both — freeze the TypeScript build before starting
   M4.
