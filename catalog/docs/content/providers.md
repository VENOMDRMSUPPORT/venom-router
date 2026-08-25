# Providers

A provider is a source or serving seller represented in the Catalog. Provider adapters declare their data sources, parsers, billing and access policy, and publish exclusions.

## What a provider owns

A provider contributes roster declarations and provider-specific facts. Shared fetch, retry, validation, delta gates, transactions, provenance, enrichment, and scoring remain in the shared pipeline rather than being reimplemented per provider.

A provider-specific detail fact must not be copied to another provider offering. The same model name at two providers can therefore have different facts, evidence, prices, or lifecycle states.

## Roster versus access

A roster listing proves that a provider declared an offering. It does not by itself prove free access or runtime health. Publish policy must fail closed, preserve excluded history, and record the reason for exclusion.

## Provider API

Use `GET /v1/providers` to read the provider list and its catalog metadata. Use `GET /v1/models?provider=<id>` to inspect offerings filtered to one provider.

Continue with [Models and Offers](/concepts/models-and-offers) to understand the distinction between an offering and a canonical model identity.
