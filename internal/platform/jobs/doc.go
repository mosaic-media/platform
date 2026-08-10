// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package jobs is the Platform's background-work runner and its scheduler.
//
// It is the answer to the case platform#13 reserved and did not build: work with
// no user behind it. A handler here is invoked by the Platform on its own
// initiative, so there is no session to forward and no principal to inherit —
// which is why the runner is handed a caller minted by the application service
// itself (the system principal) rather than constructing one.
//
// The runner owns three things and deliberately not a fourth:
//
//   - **Claiming.** One `SELECT … FOR UPDATE SKIP LOCKED` statement leases a
//     batch of runnable jobs. Two runners against one database take disjoint
//     sets without a coordinator, and a job whose runner died is reclaimed when
//     its lease lapses rather than being stranded in `running` forever.
//   - **Retrying.** A failed attempt is rescheduled by an exponential backoff
//     with a ceiling, and a job that exhausts its attempts is dead-lettered
//     rather than deleted or retried forever.
//   - **Recording.** Every attempt and every lifecycle line is written beside
//     the job, so a dead-letter can be read a fortnight later — after
//     telemetry retention has dropped the trace that produced it.
//
// What it is not is a cron expression parser. A schedule here is an interval,
// because every caller queued for it wants "about every hour" rather than
// "03:17 on Tuesdays", and a cron dialect is a vocabulary to specify, document
// and get wrong. Module-declared cron is named in the roadmap as its own piece
// of work and it starts here rather than being pre-empted.
package jobs
