# On-prem Identity

On-prem Identity governs who may participate in one installation, which Agents they own, and how those Agents authenticate. It keeps human membership separate from machine identity and product memory.

## Human access

**Installation**:
One running on-prem PAX Nexus deployment and its implicit collaboration boundary.
_Avoid_: Team tenant, organization

**User**:
A stable installation-local record for one human identity. It becomes active when linked to an external identity provider.
_Avoid_: Agent owner record, API client

**Membership**:
A User's role-bearing relationship with the Installation.
_Avoid_: User role, account

**Membership Role**:
One of Owner, Admin, or Member, governing human control-plane operations within the Installation.
_Avoid_: Agent permission, Agent type

**Owner**:
The highest Membership Role, responsible for role delegation, ownership transfer, and the last-owner invariant.
_Avoid_: Agent owner

**Human Session**:
A short-lived, revocable browser authenticator established after external identity verification.
_Avoid_: Agent credential, OIDC token

**Invitation**:
A one-time authorization for a human identity to create a Membership with a bounded role.
_Avoid_: Agent enrollment, registration key

**Bootstrap Secret**:
A one-time installation secret used only to establish the first Owner Membership.
_Avoid_: Admin API key, permanent root credential

## Agent access

**Agent**:
A stable machine identity owned by one Membership, with an immutable routing ID and mutable descriptive profile.
_Avoid_: Credential, process, session

**Agent Owner**:
The Membership that controls one Agent. A Member-role Membership may be an Agent Owner.
_Avoid_: Owner role, credential holder

**Agent Enrollment**:
A short-lived, one-time grant allowing a client installation to obtain an Agent Credential.
_Avoid_: Invitation, API key

**Agent Credential**:
A revocable machine authenticator bound to exactly one Agent and a bounded set of permissions.
_Avoid_: Agent, enrollment token

**Agent Permission**:
One capability granted to an Agent Credential, independent of the Owner's human role.
_Avoid_: Membership role, Agent type

**Routable Agent**:
An active Agent owned by an active Membership with at least one valid receive-capable credential.
_Avoid_: Registered Agent, online process
