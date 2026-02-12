# Bug: `SigVerificationDecorator` Incarnation Cache Causes State Divergence Under Block-STM

## Summary

The `SigVerificationDecorator` uses an **incarnation cache** to skip expensive signature verification when a transaction is re-executed by Block-STM. However, the cache wraps the **entire** `anteHandle` method — including **state-dependent** checks (account sequence, gas-consuming store reads) — not just the **stateless** signature verification. This causes determinism failures (app hash mismatch) between nodes running Block-STM and nodes running sequential execution.

## Background: Block-STM Re-execution

Block-STM executes transactions in parallel using optimistic concurrency. When two transactions conflict (e.g., both read/write the same account), the later transaction is **invalidated** and **re-executed** with a new **incarnation**:

```
Block with 2 txs from the same account (sequence = N):
  tx0: signed with sequence N
  tx1: signed with sequence N+1

Block-STM execution:
  Incarnation 1: tx0 and tx1 run in parallel
  → tx0 reads account (seq=N), passes, increments seq to N+1
  → tx1 reads account (seq=N, stale!), checks sig.Sequence(N+1) != acc.GetSequence()(N)
  → tx1 gets ErrWrongSequence
  
  Validation: tx1's read set is stale → tx1 is INVALIDATED
  
  Incarnation 2: tx1 re-executes
  → Now account has seq=N+1 (written by tx0)
  → Should pass: sig.Sequence(N+1) == acc.GetSequence()(N+1)
```

The **incarnation cache** is meant to avoid repeating expensive work (signature verification) across incarnations of the same transaction.

## The Bug: Caching State-Dependent Results

### Buggy Code (before fix)

```go
func (svd SigVerificationDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (newCtx sdk.Context, err error) {
    if v, ok := ctx.GetIncarnationCache(SigVerificationResultCacheKey); ok {
        // ❌ Cache HIT → skip EVERYTHING: GetSignerAcc, sequence check, VerifySignature
        if v != nil {
            err = v.(error)
        }
    } else {
        // Cache MISS → run full anteHandle (includes state-dependent checks)
        err = svd.anteHandle(ctx, tx, simulate)
        ctx.SetIncarnationCache(SigVerificationResultCacheKey, err) // ❌ caches state-dependent result
    }
    if err != nil {
        return ctx, err
    }
    return next(ctx, tx, simulate)
}
```

The `anteHandle` method does three things:

| Step | Function | State-dependent? | Safe to cache? |
|------|----------|:-:|:-:|
| 1 | `GetSignerAcc(ctx, ak, signer)` — reads account from KV store | ✅ Yes (consumes gas) | ❌ No |
| 2 | `sig.Sequence != acc.GetSequence()` — checks account sequence | ✅ Yes (reads current state) | ❌ No |
| 3 | `VerifySignature(pubKey, signerData, sig.Data)` — signature verification | ❌ No (pure computation) | ✅ Yes |

By caching the entire result, **all three steps are skipped on cache hit**. The following are two examples of how this leads to state divergence (not exhaustive):

---

## Problem 1: Stale Sequence Error → Transaction Outcome Divergence

When **two transactions from the same account** are in the same block, the incarnation cache replays a stale `ErrWrongSequence` from an earlier incarnation, causing a transaction to fail under Block-STM that would succeed under sequential execution.

### Sequence Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│ Incarnation 1: tx0 and tx1 execute in parallel                     │
├────────────────────────────────┬────────────────────────────────────┤
│           tx0                  │             tx1                    │
│                                │                                    │
│  GetSignerAcc → acc.seq = N    │  GetSignerAcc → acc.seq = N       │
│  sig.Sequence(N) == N  ✅      │  sig.Sequence(N+1) != N  ❌        │
│  VerifySignature → OK          │  → ErrWrongSequence                │
│  ... increments seq to N+1     │  → CACHED: ErrWrongSequence        │
├────────────────────────────────┴────────────────────────────────────┤
│ Validation: tx1 read stale data → tx1 INVALIDATED → re-execute     │
├─────────────────────────────────────────────────────────────────────┤
│ Incarnation 2: tx1 re-executes                                     │
│                                                                     │
│  ⚠️  Cache HIT → returns cached ErrWrongSequence                    │
│  GetSignerAcc is SKIPPED (would have read acc.seq = N+1)            │
│  Sequence check is SKIPPED (would have passed: N+1 == N+1)          │
│                                                                     │
│  Result: tx1 FAILS with code 32 (ErrWrongSequence)                  │
└─────────────────────────────────────────────────────────────────────┘

Sequential execution:
┌─────────────────────────────────────────────────────────────────────┐
│ tx0 executes first                                                  │
│  GetSignerAcc → acc.seq = N, passes, increments seq to N+1          │
├─────────────────────────────────────────────────────────────────────┤
│ tx1 executes second                                                 │
│  GetSignerAcc → acc.seq = N+1                                       │
│  sig.Sequence(N+1) == N+1  ✅ → tx1 SUCCEEDS                       │
└─────────────────────────────────────────────────────────────────────┘
```

### Consequence

| | Block-STM | Sequential |
|---|---|---|
| tx1 result | ❌ FAILS (code 32) | ✅ SUCCEEDS |
| tx1 state changes | None (rolled back) | Applied |

**→ State divergence. App hash mismatch. Consensus failure.**

---

## Problem 2: Skipped Gas Consumption → Gas Divergence

Even when transactions are from **different accounts** (no sequence conflict), the incarnation cache still causes non-determinism through gas metering differences.

When the cache hits on a re-executed incarnation, `GetSignerAcc` is skipped. This function performs a store read that **consumes gas**. Skipping it produces a lower `gas_used` for the transaction compared to sequential execution.

```
tx1 under Block-STM (re-executed, cache hit):
  GetSignerAcc SKIPPED → gas_used is lower by ~1,552 gas

tx1 under Sequential:
  GetSignerAcc runs → gas_used is higher by ~1,552 gas

Different gas_used → different execution path/state
  → App hash mismatch
```

---

## The Fix

Only cache the **stateless** `VerifySignature` result. Always re-execute the **state-dependent** parts (`GetSignerAcc`, sequence check). Use per-signer cache keys for multi-signer correctness. Construct error messages with current-incarnation values.

```go
for i, sig := range sigs {
    // ✅ ALWAYS runs: state-dependent store read (consumes gas deterministically)
    acc, err := GetSignerAcc(ctx, svd.ak, signers[i])
    if err != nil {
        return ctx, err
    }

    // ✅ ALWAYS runs: state-dependent sequence check (sees current state)
    if !isUnordered {
        if sig.Sequence != acc.GetSequence() {
            return ctx, ErrWrongSequence
        }
    }

    // ... (signerData construction using current acc, chainID, accNum) ...

    if !simulate && !ctx.IsReCheckTx() && ctx.IsSigverifyTx() {
        // Per-signer cache key prevents multi-signer collision
        sigCacheKey := fmt.Sprintf("%s:%d", SigVerificationResultCacheKey, i)
        var err error
        if v, ok := ctx.GetIncarnationCache(sigCacheKey); ok {
            if v != nil {
                err = v.(error)
            }
        } else {
            // Cache MISS: run VerifySignature and cache the raw result
            txData := adaptableTx.GetSigningTxData()
            err = authsigning.VerifySignature(ctx, pubKey, signerData, sig.Data, svd.signModeHandler, txData)
            ctx.SetIncarnationCache(sigCacheKey, err)
        }

        if err != nil {
            // Error message always uses current-incarnation values (accNum, acc.GetSequence(), chainID)
            errMsg := fmt.Sprintf("signature verification failed; please verify account number (%d) and chain-id (%s): (%s)",
                accNum, chainID, err.Error())
            return ctx, errorsmod.Wrap(sdkerrors.ErrUnauthorized, errMsg)
        }
    }
}
return next(ctx, tx, simulate)
```

### What changed

| Behavior | Before (buggy) | After (fixed) |
|---|---|---|
| `GetSignerAcc` on cache hit | ❌ Skipped (no gas) | ✅ Always runs |
| Sequence check on cache hit | ❌ Skipped (stale result) | ✅ Always re-evaluated |
| `VerifySignature` on cache hit | Skipped (cached) | Skipped (cached) ✅ |
| Cache stores | Wrapped error with baked-in account info | Raw `VerifySignature` error |
| Error message values | Stale (from caching incarnation) | Current (from this incarnation) |
| Cache key scope | Single key for all signers | Per-signer key (`:0`, `:1`, ...) |
| Multi-signer correctness | ❌ Signer 1+ skipped entirely | ✅ All signers verified |
| Gas determinism | ❌ Block-STM ≠ Sequential | ✅ Identical |
| Sequence correctness | ❌ Stale errors/successes | ✅ Current state |
