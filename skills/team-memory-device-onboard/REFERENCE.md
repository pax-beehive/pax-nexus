# Verification checklist

Run after the onboarding workflow. Every item must pass before declaring done.

## Device and paxl

```bash
paxl device status                      # device connected, expected user + provisioned count
paxl channel list                       # one profile per onboarded agent
paxl channel status <profile>           # "connected" per profile (credential verified server-side)
```

## paxm round-trip (per configured identity)

```bash
paxm remember --profile ltm --text "ONBOARD_VERIFY_<date>: <agent> onboarding check"
paxm recall --query "ONBOARD_VERIFY_<date>" --json
paxm logs --tail 1 --json
```

Pass criteria:

- `remember` refs include `team`, `provider_errors` is empty (unrelated optional
  providers may error only if the user accepts them; otherwise remove them).
- `recall` hits include an entry with `"provider": "team"`; a Team Note for the
  marker usually appears within a minute (session-batch → extraction → recall).
- `paxm config doctor` reports `ok: team`.

## Portal cross-check (optional, human)

- Admin → Devices: the device shows the expected provisioned agent count.
- Admin → Agents: each provisioned agent shows a `provisioned_by` badge.
- Pulse page: each agent card shows activity after the first observe/recall.

## Cleanup

- Delete verification markers only if the user asks; they are ordinary Team Notes.
- If the user reruns onboarding with a new device, revoke the previous device in
  the Portal first (cascades to its provisioned credentials).
