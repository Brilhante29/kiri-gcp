# Roadmap

This is a living document. It states direction, not promises; priorities shift
with what the community needs. Open an issue to propose or reprioritize.

## Now

- Deepen the highest-traffic services toward byte-exact wire fidelity: Cloud
  Storage, Pub/Sub, Firestore, BigQuery, Compute Engine.
- Keep the real-SDK scenario suite green as the definition of "drop-in".
- Publish a container image so `docker run` needs no local build.

## Next

- **Cost time machine.** A virtual clock plus a usage ledger so cost accrues from
  real provisioned resources over simulated time, not manual seeding. Query it as
  a growing daily curve.
- IAM condition and policy evaluation good enough for local authz tests.
- Broaden gRPC coverage beyond Pub/Sub and Firestore.
- Persistence format versioning and migration.

## Later

- Fault injection (latency, error rates, quota limits) for resilience testing.
- Event wiring across more services (Eventarc, Cloud Functions triggers).
- A small web console for browsing emulator state.

## Non-goals

- Being a production service or a security boundary. kiri runs with no auth by
  design, for local development and CI only.
- 100% behavioral parity with every GCP edge case. The goal is high-fidelity,
  useful local development, not a reimplementation of Google Cloud.
