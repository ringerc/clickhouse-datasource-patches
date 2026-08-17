---
description: Configure the ClickHouse data source for Grafana, including connection, TLS, logs, traces, and provisioning
labels:
products:
  - Grafana Cloud
  - Grafana OSS
  - Grafana Enterprise
keywords:
  - data source
menuTitle: Configure
title: Configure the ClickHouse data source
weight: 20
version: 0.1
last_reviewed: 2026-04-24
---

# Configure the ClickHouse data source

This page explains how to configure the ClickHouse data source, including connection settings, TLS, logs and traces column mappings, and provisioning.

## Before you begin

Before configuring the data source, ensure you have:

- **Grafana permissions:** Organization administrator role.
- **Plugin:** The ClickHouse data source plugin installed. For Grafana version compatibility, see [Requirements](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/#requirements).
- **ClickHouse:** A running ClickHouse server and a user with read-only access (or the permissions described below).
- **Network access:** The Grafana server can reach the ClickHouse server on the intended port (HTTP: 8123 or 8443 with TLS; Native: 9000 or 9440 with TLS).

{{< admonition type="note" >}}
**Grafana Cloud users:** If your ClickHouse server is behind a firewall, you must allowlist the Grafana Cloud outbound IP addresses so that queries can reach your database. For the current list of IPs, refer to [Allow Grafana Cloud outbound traffic](https://grafana.com/docs/grafana-cloud/account-management/allow-traffic/).

The published list covers standard outbound IPs but may not include every address used by your specific Grafana Cloud stack. If connections are still blocked after allowlisting the documented IPs, check your firewall or ClickHouse server logs for the rejected source addresses and [open a support ticket](https://grafana.com/profile/org#support) so the Grafana team can confirm the full set of IPs for your stack.
{{< /admonition >}}

## ClickHouse user and permissions

Grafana executes queries exactly as written and does not validate or restrict SQL. Use a **read-only ClickHouse user** for this data source to avoid accidental or destructive operations (such as modifying or deleting tables) while still allowing dashboards and queries to run.

If your ClickHouse administrator has already given you a read-only user and connection details, you can skip to [Add the data source](#add-the-data-source).

### Recommended permissions

Create a ClickHouse user with:

- **readonly** permission enabled
- Access limited to the databases and tables you intend to query
- Permission to modify the **max_execution_time** setting (required by the plugin’s client)

{{< admonition type="warning" >}}
Grafana does not prevent execution of non-read queries. If the ClickHouse user has sufficient privileges, statements such as `DROP TABLE` or `ALTER TABLE` will be executed by ClickHouse.
{{< /admonition >}}

### Configure a read-only user

To configure a suitable read-only user:

1. Create a user or profile using [Creating users and roles in ClickHouse](https://clickhouse.com/docs/en/operations/access-rights).
1. Set `readonly = 1` for the user or profile. For details, see [Permissions for queries (readonly)](https://clickhouse.com/docs/en/operations/settings/permissions-for-queries#readonly).
1. Allow modification of the **max_execution_time** setting, which is required by the [clickhouse-go](https://github.com/ClickHouse/clickhouse-go/) client so the plugin can enforce query timeouts.

#### Required SETTINGS permissions

The plugin's underlying client ([clickhouse-go](https://github.com/ClickHouse/clickhouse-go/)) sets certain ClickHouse `SETTINGS` on each query. If the ClickHouse user does not have permission to modify these settings, queries will fail at runtime even though the **Save & test** check may pass.

At a minimum the user must be allowed to change the following settings:

| Setting                | Why the plugin needs it                                   |
| ---------------------- | --------------------------------------------------------- |
| **max_execution_time** | Enforces the query timeout configured in the data source. |

When `readonly = 1` is set, ClickHouse blocks all setting changes by default. To allow the required settings without disabling read-only mode:

1. Create a [settings profile or constraint](https://clickhouse.com/docs/en/operations/settings/constraints-on-settings) for the Grafana user.
1. Set the constraint type for each required setting to **changeable_in_readonly**.

Example (SQL):

```sql
-- Allow the grafana_reader profile to modify max_execution_time while remaining read-only
ALTER SETTINGS PROFILE grafana_reader
  SETTINGS readonly = 1,
  SETTINGS max_execution_time CHANGEABLE_IN_READONLY;
```

If you see errors such as `DB::Exception: Cannot modify 'max_execution_time': Setting is locked` at query time, the user is missing this permission. Refer to [Troubleshoot ClickHouse data source issues](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/troubleshooting/) for more details.

{{< admonition type="note" >}}
If you use a **public ClickHouse instance**, do not set `readonly = 2`. Keep `readonly = 1` and use the `changeable_in_readonly` constraint described above.
{{< /admonition >}}

{{< admonition type="note" >}}
The **Enforce read-only on all queries** toggle and the per-row **Enforced** checkboxes in the data source **Custom Settings** table are a separate, additive mechanism from the server-side profile `readonly` setting. They inject `readonly=1` per-query rather than relying on the ClickHouse user's profile. For this to work, the connecting user must not already have `readonly = 1` enforced at the profile level — use `readonly = 0` or `readonly = 2` instead. See [Enforcing server-side settings](#enforcing-server-side-settings) for details.
{{< /admonition >}}

## ClickHouse protocol support

The data source supports two transport protocols: **Native** (default) and **HTTP**. Both support the same query capabilities. The Native protocol uses ClickHouse's binary TCP interface for better performance. HTTP uses the ClickHouse HTTP interface, which is useful when your network requires HTTP-based connectivity (for example, through a reverse proxy or load balancer).

### Default ports

| Protocol | TLS | Port |
| -------- | --- | ---- |
| HTTP     | No  | 8123 |
| HTTP     | Yes | 8443 |
| Native   | No  | 9000 |
| Native   | Yes | 9440 |

When you enable **Secure connection** in Grafana, you must also set the port to a TLS-enabled port. Grafana does not change the port automatically when TLS is toggled on.

## Add the data source

To add the data source:

1. Click **Connections** in the left-side menu.
1. Click **Add new connection**.
1. Type **ClickHouse** in the search bar.
1. Select **ClickHouse**.
1. Click **Add new data source**.

## Configure settings

After adding the data source, configure the following settings.

### Server settings

| Setting               | Description                                                                                                                                             |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Name**              | The name used to refer to the data source in panels and queries.                                                                                        |
| **Default**           | Toggle to make this the default data source for new panels.                                                                                             |
| **Server**            | The ClickHouse server host (for example, `localhost`).                                                                                                  |
| **Protocol**          | **Native** or **HTTP**.                                                                                                                                 |
| **Port**              | Port number; depends on protocol and whether TLS is enabled (see default ports above).                                                                  |
| **Secure connection** | Enable when your ClickHouse server uses TLS. When enabled, update the **Port** to a TLS-enabled port and configure [TLS settings](#tls-settings) below. |
| **Username**          | ClickHouse user name. Use a [read-only user](#clickhouse-user-and-permissions).                                                                         |
| **Password**          | ClickHouse user password.                                                                                                                               |
| **Forward OAuth Identity** | Forward the logged-in Grafana user's OAuth token to ClickHouse as a JWT instead of authenticating with the configured username and password. ClickHouse Cloud only; requires a [secure (TLS) connection](#tls-settings). See [Forward OAuth Identity](#forward-oauth-identity). |
| **Default database**  | The database the query builder uses when no database is selected. If left blank, the plugin defaults to `default`.                                      |
| **Default table**     | The default table used by the query builder.                                                                                                            |

### Default database guidance

The **Default database** setting controls which database the query builder and ad hoc filters use when no database is explicitly specified.

- **Self-hosted ClickHouse:** Set this to the database you query most often so that the query builder pre-selects it.
- **ClickHouse Cloud:** Leave this field **blank**. ClickHouse Cloud connections already route to the correct default database for your service. Setting an explicit value can cause `Unknown database` errors if the name does not match the service's configured database.

If you are unsure which database to use, leave the field blank and select a database per query in the query builder.

### HTTP settings

The following settings appear only when **Protocol** is set to **HTTP**:

| Setting                          | Description                                                                                                                                                                                   |
| -------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **HTTP URL Path**                | Additional URL path appended to HTTP requests (for example, `/clickhouse`). Defaults to `/`.                                                                                                  |
| **Custom HTTP headers**          | Static headers sent with every request. Each header has a name, value, and an optional **Secure** toggle that stores the value in encrypted storage.                                          |
| **Forward Grafana HTTP headers** | When enabled, forwards Grafana request headers (such as authentication headers) to ClickHouse. Enables multi-connection mode so each unique set of forwarded headers gets its own connection. |

### TLS settings

When **Secure connection** is enabled, the following TLS settings become available:

| Setting             | Description                                                                                                                    |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| **Skip TLS Verify** | Skip server certificate verification. Use only for testing; not recommended for production.                                    |
| **TLS Client Auth** | Enable mutual TLS (mTLS) by providing a client certificate and key.                                                            |
| **With CA Cert**    | Provide a custom CA certificate for verifying the ClickHouse server's TLS certificate (required for self-signed certificates). |
| **CA Cert**         | PEM-encoded CA certificate.                                                                                                    |
| **Client Cert**     | PEM-encoded client certificate (required when TLS Client Auth is enabled).                                                     |
| **Client Key**      | PEM-encoded client private key (required when TLS Client Auth is enabled).                                                     |

### Configuration mode

Use **Configuration mode** to choose how the data source is used by the query builder.

| Mode              | Description                                                                                                                                                  |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **All databases** | Allows queries against any database and table the ClickHouse user can read. Use this mode for general exploration and dashboards that query multiple tables. |
| **Single source** | Focuses the data source on one logs or traces table. Choose a **Signal type**, then configure the database, table, and column mappings for that source.      |

Use **Single source** when the data source is dedicated to one logs or traces table, such as an OpenTelemetry table. This keeps the logs or traces schema settings with the selected source and avoids reconfiguring column mappings in each query. Single source data sources use the [compact query mode](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/query-editor/#compact-query-mode) in the query editor.

### Additional settings

| Setting              | Description                                                           |
| -------------------- | --------------------------------------------------------------------- |
| **Dial Timeout**     | Timeout in seconds for establishing a connection. Default: `10`.      |
| **Query Timeout**    | Timeout in seconds for read queries. Default: `60`.                   |
| **Validate SQL**     | When enabled, validates SQL syntax in the query editor.               |
| **Enable row limit** | When enabled, applies the Grafana row limit setting to query results. |

### Custom ClickHouse settings

You can pass arbitrary ClickHouse `SETTINGS` with every query by adding key-value pairs in the **Custom Settings** section. For example, you can set `max_block_size` or `max_threads` to tune query performance.

These settings are appended to each query's `SETTINGS` clause. They do not replace any settings that the plugin sets internally (such as `max_execution_time`). To make specific settings tamper-resistant so that end users cannot override them in their SQL, see [Enforcing server-side settings](#enforcing-server-side-settings).

### Enforcing server-side settings

The **Enforced** checkbox on each Custom Setting row enables a tamper-resistant mode: the datasource sends the setting alongside `readonly=1` on every query, so the end user's SQL cannot override it with a `SETTINGS` or `SET` clause. This is the mechanism the plugin uses to bind a per-request tenant identifier (or any other operator-controlled value) into ClickHouse row policies, without rewriting user SQL and without provisioning per-tenant database users.

When any custom setting is marked **Enforced**, the **Enforce read-only on all queries** toggle is automatically enabled. You can also enable that toggle independently to make the datasource fully read-only (SELECT/SHOW only) without any enforced settings.

#### How enforcement works

The plugin injects the enforced settings and `readonly=1` as per-query settings out-of-band with the SQL — via HTTP query-string parameters or the Native protocol's per-query settings block. ClickHouse enforces two invariants that make this tamper-resistant:

1. `readonly` can only be increased per query; a user's `SETTINGS readonly=0` is rejected once it is already `1`.
2. Under `readonly=1`, any `SETTINGS foo=…` or `SET foo=…` in the user's SQL that attempts to change a non-allowlisted setting is rejected outright.

**Caveats that apply to every enforced setting, regardless of value source:**

- **Enforced settings must NOT be marked `CHANGEABLE_IN_READONLY` on the ClickHouse server.** If they are, a user can override them even under `readonly=1`, which collapses the enforcement guarantee. The `<changeable_in_readonly/>` server-side tag is intended only for tunables like `max_threads` and `max_memory_usage` that operators want to allow users to tune per query.
- Enabling this feature makes the datasource **read-only**: INSERT, CREATE, ALTER, and other write statements from Grafana will be rejected by ClickHouse.
- The connecting DB user must start at `readonly=0` (or `readonly=2`). If the user's server profile already enforces `readonly=1`, the plugin cannot inject the enforced setting values before that restriction takes effect.

#### Value sources at a glance

Every enforced setting has a **Source** that determines where the per-query value comes from:

| Source (UI) | `source` (provisioning) | Value comes from                                  | Per-request? | Trust root                                                  | Typical use case                                                                 |
| ----------- | ----------------------- | ------------------------------------------------- | ------------ | ----------------------------------------------------------- | -------------------------------------------------------------------------------- |
| **Static**  | `static` (or omitted)   | The **Value** field in the datasource config      | No           | Whoever can edit the datasource config (org admin)          | One value shared by every query on this datasource instance                      |
| **Request header** | `header`         | A named HTTP request header on the incoming query | Yes          | A trusted upstream proxy that stamps the header             | Per-user or per-tenant values injected by an identity-aware gateway              |
| **JWT claim** | `jwt`                 | A claim inside a JWT carried by a named header    | Yes          | The JWT issuer (Grafana itself, or an external identity provider (IdP) via JWKS)| Per-user or per-tenant values whose integrity is cryptographically verifiable    |

The remainder of this section describes each source in detail, then summarizes the global Grafana and per-datasource settings that must be aligned for header- and JWT-based bindings to work.

#### Where to configure

Enforced settings can be configured through the datasource UI (Grafana **Connections > Data sources > _your ClickHouse datasource_ > Settings**) or through a [datasource provisioning file](https://grafana.com/docs/grafana/latest/administration/provisioning/#data-sources). The three UI locations you touch are:

1. The **Custom Settings** section — one row per enforced binding. Each row is one entry under `jsonData.customSettings` in provisioning.
2. The **Enforce read-only on all queries** toggle (below the Custom Settings table) — implied whenever any row has **Enforced** checked. In provisioning this is `jsonData.enforceReadOnly` (boolean).
3. The **HTTP settings > Forward Grafana HTTP headers** toggle — required for most header- and JWT-based bindings; see [Grafana settings that affect enforced bindings](#grafana-settings-that-affect-enforced-bindings). In provisioning this is `jsonData.forwardGrafanaHeaders` (boolean).

The mapping between UI fields on a Custom Setting row and provisioning keys is:

| UI field         | Provisioning key (`jsonData.customSettings[].`) | Applies to source    | Notes                                                                                       |
| ---------------- | ----------------------------------------------- | -------------------- | ------------------------------------------------------------------------------------------- |
| **Setting**      | `setting`                                       | all                  | The ClickHouse setting name (e.g. `custom_visible_tenants`).                                |
| **Value**        | `value`                                         | static               | Must be **empty** for `header` and `jwt` sources.                                           |
| **Enforced**     | `enforced` (bool)                               | all                  | For `static`, optional: when `false`, the value is appended as an ordinary custom setting the user can override in SQL. For `header` and `jwt`, must be `true`; a dynamic source with `enforced=false` is rejected at load time. |
| **Source**       | `source`                                        | all                  | `static` (default), `header`, or `jwt`.                                                     |
| **Header name**  | `headerName`                                    | header               | Required; canonicalised via `http.CanonicalHeaderKey`. Multi-valued headers are rejected.   |
| **Token header** | `jwtHeaderName`                                 | jwt                  | Defaults to `X-Grafana-Id` when blank. Grafana's `X-Grafana-Id` is a re-minted token that carries only a small set of Grafana-native claims — for IdP-native claims (groups, email, tenant claims) point at `X-Id-Token` or a custom header and enable **Forward Grafana HTTP Headers**. See [Choosing the token header](#choosing-the-token-header). |
| **Claim path**   | `jwtClaim`                                      | jwt                  | Required. Dotted path (e.g. `tenants` or `app.tenant.id`).                                  |
| **Array join**   | `jwtClaimJoin`                                  | jwt                  | Separator for array-valued claims. Defaults to `,`.                                         |
| **Verify**       | `jwtVerify`                                     | jwt                  | `none` (default) or `jwks`. See [Signature verification](#jwt-signature-verification).      |
| **JWKS URL**     | `jwtJwksUrl`                                    | jwt (verify=jwks)    | Required when `jwtVerify: jwks`.                                                            |
| **Issuer**       | `jwtIssuer`                                     | jwt (verify=jwks)    | Optional. Adds `iss` check.                                                                 |
| **Audience**     | `jwtAudience`                                   | jwt (verify=jwks)    | Optional. Adds `aud` check.                                                                 |
| **On missing**   | `onMissing`                                     | header, jwt          | `reject` (default) or `empty`. See [Missing-value handling](#missing-value-handling).       |

#### Static-value source

A static-source enforced setting sends the same operator-supplied value with every query.

Configure it by adding a Custom Setting row with **Setting**, **Value**, and **Enforced** checked; leave **Source** on **Static** (the default).

```yaml
jsonData:
  customSettings:
    - setting: custom_visible_tenants
      value: 't1,t2'          # the tenants this datasource instance may see
      enforced: true
      # source: static        # optional; this is the default
```

**Worked example — row-level multi-tenancy:**

```sql
-- Create a row policy that reads the enforced setting
CREATE ROW POLICY tenant_filter ON mydb.events
  USING has(splitByChar(',', getSetting('custom_visible_tenants')), tenant_id)
  TO grafana_user;
```

With `readonly=1` active, the user cannot change `custom_visible_tenants` in their query, so the row policy always filters on the operator-supplied tenant list. Deploy one datasource instance per tenant scope (or per set of tenants); users get access to a scope by being granted access to the corresponding datasource in Grafana.

#### Request-header source

A header-source enforced setting reads its value from a named HTTP request header on each incoming query. This lets a trusted upstream proxy inject per-user or per-tenant values (for example, an identity-aware gateway that sets `X-Allowed-Projects` based on the authenticated user's entitlements).

To configure it, set **Source** to **Request header**, fill in **Header name**, and leave **Value** empty. Choose an **On missing** policy.

```yaml
jsonData:
  customSettings:
    - setting: custom_visible_tenants
      value: ''
      enforced: true
      source: header
      headerName: X-Allowed-Projects
      onMissing: reject
```

For this source to work end-to-end, the header must reach the plugin. In almost all cases that means enabling **Forward Grafana HTTP Headers** on the datasource; see [Grafana settings that affect enforced bindings](#grafana-settings-that-affect-enforced-bindings) below. Multi-valued headers are rejected with an error to prevent ambiguous concatenation.

**Trust model — this mode requires a trusted upstream proxy.** Nothing in the plugin prevents a browser from supplying arbitrary header values unless a trusted proxy unconditionally overwrites the header on every request before it reaches Grafana. Only use this mode when:

1. All traffic to Grafana passes through a proxy that you control.
2. That proxy **unconditionally** sets the header on every request (not merely adds it when absent).
3. The proxy enforces authentication before setting the header.

If any of those guarantees does not hold, prefer the JWT-claim source with `jwtVerify: jwks`.

#### JWT-claim source

A JWT-claim-source enforced setting reads its value from a claim inside a JWT carried by a named HTTP header. This provides a stronger trust model than plain header binding because the token's contents can be cryptographically verified against a JWKS endpoint.

To configure it, set **Source** to **JWT claim**, fill in **Claim path**, and choose a **Token header** and **Verify** mode.

```yaml
jsonData:
  customSettings:
    - setting: custom_visible_tenants
      value: ''
      enforced: true
      source: jwt
      jwtHeaderName: X-Grafana-Id   # optional; defaults to X-Grafana-Id
      jwtClaim: tenants             # required
      jwtClaimJoin: ','             # optional; defaults to ","
      jwtVerify: none               # "none" (default) or "jwks"
      onMissing: reject             # "reject" (default) or "empty"
```

Grafana does **not** have to be configured to use JWT for user authentication (`[auth.jwt]`) in order to bind an enforced setting to an external IdP JWT. Enforced-settings JWT verification is entirely independent of Grafana's login-time JWT pipeline: as long as the token reaches the plugin in a forwarded header (typically `X-Id-Token` via **Forward Grafana HTTP Headers**, or a custom header), the plugin will parse and — with `jwtVerify: jwks` — cryptographically verify it against the JWKS URL you configure here, regardless of how users authenticate to Grafana itself.

##### Choosing the token header

Different token headers carry different sets of claims. Choose the one whose claim you actually need:

| Header             | Minted / forwarded by                        | Claim shape                                                                                          | Typical use                                                                                       |
| ------------------ | -------------------------------------------- | ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| `X-Grafana-Id` (default) | Grafana core (re-minted per request); requires the `idForwarding` feature toggle to be enabled in `grafana.ini`, otherwise this header is never sent to the plugin | Grafana-native: `sub` (e.g. `user:1`), `iss`, `iat`, `exp`, `namespace`, `authenticated_by`. No IdP-native claims. | Bind to Grafana's identity of the user (username, org, tenant when Grafana knows it).             |
| `X-Id-Token`       | Grafana forwards the cached upstream OIDC ID token when header forwarding is on | Full IdP-native claim set (`groups`, `email`, `preferred_username`, custom tenant claims, etc.).      | Bind to a group, role, or tenant claim minted by your IdP.                                        |
| `Authorization`    | Forwarded from the browser's OAuth session (via **Forward OAuth Identity** or the general header-forwarding toggle) | Whatever the IdP put in the OAuth access token (often opaque, sometimes a JWT).                     | Rarely useful — access tokens are typically opaque and unstable. Prefer `X-Id-Token`.             |
| Any custom header  | An upstream proxy or auth-proxy setup        | Whatever the proxy signs into it.                                                                    | Auth-proxy deployments, per-request JWTs minted by an API gateway.                                |

`X-Grafana-Id` is the safest default because Grafana signs and re-mints it per request, but it is deliberately **sanitized** — it carries only Grafana-native claims (`sub`, `iss`, `iat`, `exp`, `namespace`, `authenticated_by`) and drops everything the upstream IdP put in the original token. To bind on IdP-native claims like `groups`, `email`, `preferred_username`, or a tenant claim, point the binding at `X-Id-Token` (the cached upstream OIDC ID token, forwarded verbatim) or a custom header, enable **Forward Grafana HTTP Headers** on the datasource so the header actually reaches the plugin, and enable JWKS verification. See [Grafana settings that affect enforced bindings](#grafana-settings-that-affect-enforced-bindings) for the exact toggles.

##### JWT signature verification

The **Verify** mode controls whether the plugin checks the token's signature:

| Mode    | Meaning                                                                                          | Required extra fields                          | Extra runtime checks                                                                                                    |
| ------- | ------------------------------------------------------------------------------------------------ | ---------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `none`  | Trust the forwarded token without checking its signature.                                        | none                                           | For non-`X-Grafana-Id` sources, the plugin enforces the `exp` claim with 60 s leeway; expired tokens are treated as absent. |
| `jwks`  | Fetch keys from a JWKS endpoint and verify the signature; also runs default `exp`/`nbf`/`iat` checks. | `jwtJwksUrl`. Optional: `jwtIssuer`, `jwtAudience`. | JWKS is cached and refreshed every 15 minutes; failed fetches are cached for 30 s. On failure the query is rejected with a categorized error (e.g. "signature invalid", "unknown kid", "jwks fetch failed"). |

```yaml
      jwtVerify: jwks
      jwtJwksUrl: https://idp.example/.well-known/jwks.json   # required
      jwtIssuer: https://idp.example                          # optional
      jwtAudience: grafana                                    # optional
```

Use `jwtVerify: jwks` whenever the token comes from an external IdP, or whenever you want defence-in-depth against key rotation, revocation, and in-transit tampering between Grafana and out-of-process plugin binaries. Verify `X-Grafana-Id` against Grafana's `/api/signing-keys/keys` endpoint (`https://<your-grafana>/api/signing-keys/keys`).

{{< admonition type="note" >}}
**JWT verification is independent of Grafana's login-time JWT auth.** Both `jwtVerify: none` and `jwtVerify: jwks` operate entirely inside the plugin: `none` parses the token without a signature check, and `jwks` fetches the key set from `jwtJwksUrl` and verifies against it (plus optional `iss`/`aud` checks). Neither mode consults Grafana's `[auth.jwt]` configuration or its login pipeline, so passing verification here does **not** imply Grafana would accept the same token to log a user in. Conversely, you can bind an enforced setting to a JWT (for example a forwarded OIDC ID token) even when Grafana is not configured to use JWT for login at all — the user just needs to be signed in to Grafana by any means, and a header carrying the token needs to reach the plugin.
{{< /admonition >}}

{{< admonition type="note" >}}
**Freshness under `jwtVerify: none`.** Grafana validates upstream OAuth tokens **only at login**; it does not re-verify or re-mint them at query time. Forwarded IdP tokens (in `Authorization`, `X-Id-Token`, or any custom header carrying an upstream token) are read from Grafana's cache and can outlive their `exp` by hours. To prevent a stale claim silently binding to a server-side setting, the plugin enforces the `exp` claim (with 60 s leeway) even under `jwtVerify: none`, **except** when the token comes from `X-Grafana-Id` — which Grafana re-mints per request and is Grafana's concern to keep fresh. An expired token is treated as if the value were absent, so the `onMissing` policy (`reject` or `empty`) decides the outcome.
{{< /admonition >}}

##### Missing-value handling

The **On missing** field controls what happens when a dynamic source produces no value — the header is absent, the token is malformed, the claim path does not resolve, or (under `verify=none`) the token has expired:

| Setting (UI)       | `onMissing`   | Behaviour                                                                            |
| ------------------ | ------------- | ------------------------------------------------------------------------------------ |
| **Reject query**   | `reject` (default) | The query is rejected with a descriptive error before it reaches ClickHouse.    |
| **Treat as empty** | `empty`       | The plugin sends an empty string (`''`) as the setting value. Row policies that use `has(splitByChar(',', ...), x)` will match zero rows in this case, which is usually the safe default. |

Signature-verification failures under `jwtVerify: jwks` (bad signature, `iss`/`aud` mismatch, unknown key ID, JWKS fetch failure) are never treated as "missing" — they always reject the query, regardless of `onMissing`.

**Example — bind `custom_visible_tenants` to the `tenants` claim in `X-Grafana-Id`, no signature verification:**

```yaml
jsonData:
  customSettings:
    - setting: custom_visible_tenants
      value: ''
      enforced: true
      source: jwt
      jwtHeaderName: X-Grafana-Id
      jwtClaim: tenants
      jwtVerify: none
      onMissing: reject
```

**Example — bind to a `tenants` claim in a forwarded OIDC ID token, verified against an external IdP:**

```yaml
jsonData:
  customSettings:
    - setting: custom_visible_tenants
      value: ''
      enforced: true
      source: jwt
      jwtHeaderName: X-Id-Token
      jwtClaim: tenants
      jwtVerify: jwks
      jwtJwksUrl: https://idp.example/.well-known/jwks.json
      jwtIssuer: https://idp.example
      jwtAudience: grafana
      onMissing: reject
```

#### Grafana settings that affect enforced bindings

Header- and JWT-sourced bindings depend on Grafana forwarding the right HTTP headers to the plugin. The relevant knobs live in two places: the `idForwarding` feature toggle in `grafana.ini` (see the `X-Grafana-Id` row in [Choosing the token header](#choosing-the-token-header)) and the per-datasource toggles listed below.

**Which headers Grafana forwards to backend plugins:**

| Header                                                       | Forwarded by default? | Extra Grafana configuration required                                                             |
| ------------------------------------------------------------ | --------------------- | ------------------------------------------------------------------------------------------------ |
| `X-Grafana-Id`                                               | Yes                   | `idForwarding` feature toggle must be enabled (`[feature_toggles] idForwarding = true`).         |
| `X-Dashboard-Uid`, `X-Panel-Id`, `X-Rule-Uid`, `X-Datasource-Uid` | Yes                   | None.                                                                                            |
| `Authorization`                                              | No                    | Datasource **Forward OAuth Identity** toggle, **or** datasource **Forward Grafana HTTP Headers** toggle. |
| `X-Id-Token`                                                 | No                    | Datasource **Forward Grafana HTTP Headers** toggle.                                              |
| `Cookie`                                                     | No                    | Datasource **Forward Grafana HTTP Headers** toggle.                                              |
| `X-Grafana-User`                                             | No                    | Datasource **Forward Grafana HTTP Headers** toggle. This plugin injects `X-Grafana-User` from the plugin's request context when the toggle is on. |
| Any custom header (e.g. `X-Allowed-Projects`, custom JWT header) | No                    | Datasource **Forward Grafana HTTP Headers** toggle.                                              |

**Datasource-level toggles that matter for enforced bindings:**

| Toggle (UI) | Provisioning key                     | Effect                                                                                                                                                  |
| ----------- | ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Forward Grafana HTTP Headers** | `jsonData.forwardGrafanaHeaders` (bool) | Forwards the current request's headers (beyond the small always-forwarded set) to the plugin. Required for almost every `header`/`jwt` binding. |
| **Forward OAuth Identity**       | `jsonData.oauthPassThru` (bool)         | Forwards the browser's OAuth `Authorization` header to the plugin. Independent of the general header-forwarding toggle.                          |
| **Enforce read-only on all queries** | `jsonData.enforceReadOnly` (bool)   | Sends `readonly=1` on every query even when no setting is marked **Enforced**. Automatically implied when any row is enforced.                    |

{{< admonition type="warning" >}}
**"Forward Grafana HTTP Headers" is required for most bindings.** If a `header`- or `jwt`-sourced enforced binding points at a header outside the always-forwarded set (`X-Grafana-Id`, `X-Dashboard-Uid`, `X-Panel-Id`, `X-Rule-Uid`, `X-Datasource-Uid`) and the toggle is off, the datasource's **Save & Test** health check fails with a clear error so the misconfiguration surfaces before end users hit it.
{{< /admonition >}}

#### Save & Test health checks

When you press **Save & Test** on a datasource with enforced settings configured, the plugin runs these checks in addition to the standard connectivity probe:

1. **Header-forwarding gate.** If any dynamic binding points at a header outside the always-forwarded set and **Forward Grafana HTTP Headers** is off, the health check fails immediately with a message naming the offending settings and headers.
2. **Enforced-setting round trip.** The plugin issues a probe query with the enforced settings and `readonly=1` applied, and verifies that ClickHouse actually rejects a user attempt to override them (server-side exception code 164). This catches `CHANGEABLE_IN_READONLY` misconfiguration on the server side.
3. **JWKS reachability** (per `jwt` binding with `jwtVerify: jwks`). The plugin fetches the JWKS URL, parses it, and requires a non-empty key set. Failures surface as a health error so they do not silently break queries at runtime.

Use **Save & Test** after every configuration change; it catches the common misconfigurations that would otherwise only appear when a real user runs a real query.

### Logs configuration

The data source includes a dedicated configuration section for log queries. These settings control the default column mappings used by the [logs query builder](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/query-editor/#logs-query-builder):

| Setting                  | Description                                                                                                                                                                                                                                                 |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Default log database** | The default database for log queries.                                                                                                                                                                                                                       |
| **Default log table**    | The default table for log queries.                                                                                                                                                                                                                          |
| **Use OTel**             | When enabled, pre-fills column mappings for [OpenTelemetry ClickHouse exporter](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/exporter/clickhouseexporter) tables. Select the OTel schema version that matches your exporter. |
| **Time column**          | The high-precision timestamp column for sorting log rows.                                                                                                                                                                                                   |
| **Filter Time column**   | A lower-precision time column for fast partition-based filtering. Used with the `1.2.9` schema only.                                                                                                                                                        |
| **Log Level column**     | The column containing the log severity level.                                                                                                                                                                                                               |
| **Log Message column**   | The column containing the log message body.                                                                                                                                                                                                                 |
| **Context columns**      | Comma-separated list of columns included alongside log messages for additional context.                                                                                                                                                                     |

When **Configuration mode** is set to **Single source** and **Signal type** is set to **Logs**, these settings define the focused logs source.

### Traces configuration

The data source includes a dedicated configuration section for trace queries. These settings control the default column mappings used by the [traces query builder](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/query-editor/#traces-query-builder):

| Setting                    | Description                                                                                                                  |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| **Default trace database** | The default database for trace queries.                                                                                      |
| **Default trace table**    | The default table for trace queries.                                                                                         |
| **Use OTel**               | When enabled, pre-fills column mappings for OpenTelemetry tables. Select the OTel schema version that matches your exporter. |
| **Duration unit**          | The unit for the duration column (`seconds`, `milliseconds`, `microseconds`, or `nanoseconds`).                              |
| **Flatten nested**         | Enable if your traces table was created with `flatten_nested=1`.                                                             |

When **Use OTel** is disabled, you can manually configure columns for Trace ID, Span ID, Parent Span ID, Service Name, Operation Name, Start Time, Duration, Tags, Service Tags, Kind, Status Code, Status Message, State, and Instrumentation Library.

When **Configuration mode** is set to **Single source** and **Signal type** is set to **Traces**, these settings define the focused traces source.

### OTel schema versions

The plugin ships built-in column maps for the OpenTelemetry ClickHouse exporter's default schemas. Pick the version that matches the exporter that wrote your data:

- **`auto (latest)`** — detects the logs schema version from the table's columns when building a query: tables with a `TimestampTime` column use the `1.2.9` map, tables without it use the `1.3.0` map. Recommended, and the default. Pin a specific version below to override the detection.
- **`1.3.0`** — `opentelemetry-collector-contrib` clickhouseexporter `v0.151.0` and later. The `otel_logs` table partitions and orders directly on `Timestamp`. The `TimestampTime` column was removed in [PR #47720](https://github.com/open-telemetry/opentelemetry-collector-contrib/pull/47720), so the **Filter Time column** is left blank.
- **`1.2.9`** — `opentelemetry-collector-contrib` clickhouseexporter `v0.150.x` and earlier. The `otel_logs` table includes a `TimestampTime DateTime` column used for partition-based filtering.

The trace tables (`otel_traces`, `otel_traces_trace_id_ts`) and metric tables are unchanged across these versions; only the `otel_logs` schema changed. Detection therefore only applies to logs, and traces always use the selected version's map.

If queries fail with an `Unknown identifier 'TimestampTime'` error after upgrading the exporter, leave the version on `auto (latest)` and rebuild the query, or pin the schema version to `1.3.0`.

### Private data source connect

{{< admonition type="note" >}}
Only available for Grafana Cloud users.
{{< /admonition >}}

Private data source connect (PDC) allows you to establish a private, secured connection between a Grafana Cloud instance (or stack) and data sources secured within a private network. Select the drop-down to locate the URL for PDC. For more information, refer to [Private data source connect](https://grafana.com/docs/grafana-cloud/connect-externally-hosted/private-data-source-connect/).

Click **Manage private data source connect** to go to your PDC connection page, where you can find your PDC configuration details.

## Verify the connection

Once you have configured your ClickHouse connection settings, click **Save & test** to verify the connection. When the connection test succeeds, you see **Data source is working**. A successful test confirms that Grafana can reach ClickHouse and that the credentials are valid.

If the test fails, refer to [Troubleshoot ClickHouse data source issues](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/troubleshooting/) for common configuration errors and solutions.

## Forward Grafana HTTP headers

When you use the **HTTP** protocol, you can propagate Grafana's per-request HTTP headers end-to-end to ClickHouse. This attaches context about the originating Grafana user, dashboard, and panel to every query so you can drive query-log attribution, quotas, and row policies from ClickHouse.

To enable it, expand **Optional HTTP settings** on the data source and turn on **Forward Grafana HTTP headers to data source**.

{{< admonition type="note" >}}
This setting is only available on the HTTP protocol. The native protocol does not carry HTTP headers.
{{< /admonition >}}

When the toggle is on, the following headers are forwarded on each ClickHouse connection:

| Header                                                                                     | Source                                                                                                                                                                          |
| ------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `X-Grafana-User`                                                                           | The logged-in Grafana user's login. Populated from the plugin's request context, so you do not need to enable the Grafana `dataproxy.send_user_header` setting for this plugin. |
| `X-Dashboard-Uid`, `X-Panel-Id`, `X-Panel-Plugin-Id`, `X-Dashboard-Title`, `X-Panel-Title` | Identifiers set by Grafana when the query originates from a dashboard panel.                                                                                                    |
| `X-Grafana-Org-Id`, `X-Query-Group-Id`, `X-Grafana-From-Expr`, `X-Datasource-Uid`          | Request-context headers set by Grafana core.                                                                                                                                    |

### Use cases

- **Query-log attribution** — ClickHouse records the forwarded headers in `system.query_log.http_user_agent` and related fields, so operators can correlate queries back to the Grafana user and dashboard that triggered them.
- **Row policies and quotas** — ClickHouse [row policies](https://clickhouse.com/docs/en/operations/access-rights/#row-policies) and [quotas](https://clickhouse.com/docs/en/operations/quotas) can key on the `X-Grafana-User` header, so a single shared ClickHouse account can still enforce per-viewer access rules. For a stronger, tamper-resistant, and more flexible mechanism — including per-request tenant claims sourced from a JWT or a signed proxy header, and settings the end user cannot override in their SQL — see [Enforcing server-side settings](#enforcing-server-side-settings).

### Connection pool implications

With header forwarding enabled, connections are keyed by the forwarded header set, which means each distinct Grafana user opens their own ClickHouse connection. Expect the connection count to scale with concurrent unique users and size `max_connections` on your ClickHouse server accordingly.

### Custom headers

To forward headers other than the Grafana-set ones — for example, bearer tokens or tenant identifiers — add them as **Custom HTTP headers** in the same **Optional HTTP settings** panel. Custom headers are sent on every query regardless of the **Forward Grafana HTTP headers** toggle.

## Forward OAuth Identity

{{< admonition type="note" >}}
JWT authentication requires server-side support for JSON Web Tokens, which is currently [ClickHouse Cloud only](https://clickhouse.com/docs/en/operations/external-authenticators/jwt). It is not available on self-hosted or open source ClickHouse.
{{< /admonition >}}

When **Forward OAuth Identity** is enabled, the plugin forwards the logged-in Grafana user's OAuth access token to ClickHouse as a JSON Web Token (JWT). ClickHouse then authenticates each query as the real user rather than as a shared service account, so you can drive per-user access rights, [row policies](https://clickhouse.com/docs/en/operations/access-rights/#row-policies), and query-log attribution from ClickHouse-side identities.

For the token to be available, Grafana must be configured to forward the user's OAuth access token to the data source. For server-side setup, refer to [ClickHouse JWT authentication](https://clickhouse.com/docs/en/operations/external-authenticators/jwt).

To enable it, turn on **Forward OAuth Identity** in the **Database credentials** section of the data source configuration.

### Requirements and behavior

- **A secure (TLS) connection is required.** Enable **Secure connection** and set the **Port** to a TLS-enabled port. The plugin rejects the connection if JWT authentication is enabled without TLS. It also rejects the connection when **Skip TLS Verify** is enabled, because forwarding a real user's token over an unverified connection exposes it to interception.
- **Credentials are suppressed on query connections.** When enabled, the configured username and password are not sent on per-query connections; the forwarded token is the sole credential.
- **Health checks fall back to username and password.** **Save & test** and other health checks run outside a user request, where no user token is available, so they use the configured username and password. Keep valid credentials configured so connection tests can succeed.
- **Alerting is blocked by default.** Alert rule evaluation also runs outside a user session, so there is no identity to forward. By default these queries are **rejected** rather than run as a shared account. To keep alerting working, enable **Allow service account fallback** in the **Database credentials** section.

  {{< admonition type="warning" >}}
  Enabling **Allow service account fallback** lets alert rules and other backend queries fall back to the configured username and password. Those queries then authenticate as the shared service account and are **not** subject to the per-user [row policies](https://clickhouse.com/docs/en/operations/access-rights/#row-policies), quotas, or query-log attribution that OAuth pass-through enforces for interactive queries — so an alert may read rows a given dashboard user could not see interactively. Scope the configured service account to the least privilege your alert queries require. The plugin emits a backend warning log each time this fallback is exercised.
  {{< /admonition >}}
- **Connections are keyed per user.** Enabling JWT authentication automatically turns on header forwarding, so each Grafana user opens a separate ClickHouse connection. See [Connection pool implications](#connection-pool-implications) for sizing guidance.

## Provision the data source

You can define the data source in YAML files as part of the Grafana provisioning system. For more information, refer to [Provisioning Grafana data sources](https://grafana.com/docs/grafana/latest/administration/provisioning/#data-sources).

Example ClickHouse data source configuration with basic authentication:

```yaml
apiVersion: 1
datasources:
  - name: ClickHouse
    type: grafana-clickhouse-datasource
    jsonData:
      host: localhost
      port: 9000
      protocol: native
      username: grafana_reader
      # configMode: classic          # "classic" for all databases, "single-table" for single source
      # signalType: logs             # "logs" or "traces"; used when configMode is "single-table"
      # defaultDatabase: <string>
      # defaultTable: <string>
      # logs:
      #   defaultDatabase: <string>
      #   defaultTable: <string>
      #   otelEnabled: <bool>
      # traces:
      #   defaultDatabase: <string>
      #   defaultTable: <string>
      #   otelEnabled: <bool>
      # secure: <bool>
      # tlsSkipVerify: <bool>
      # tlsAuth: <bool>
      # tlsAuthWithCACert: <bool>
      # dialTimeout: <seconds>
      # queryTimeout: <seconds>
      # validateSql: <bool>
      # enableRowLimit: <bool>
      # forwardGrafanaHeaders: <bool>
      # oauthPassThru: <bool>  # forward the user's OAuth token as a JWT (ClickHouse Cloud only); requires secure: true
      # path: <string>  # HTTP URL path (HTTP protocol only)
      # httpHeaders:     # HTTP protocol only
      #   - name: X-Example-Header
      #     secure: false
      #     value: <string>
      # customSettings:
      #   - setting: max_block_size
      #     value: "65505"
    secureJsonData:
      password: password
      # tlsCACert: <string>
      # tlsClientCert: <string>
      # tlsClientKey: <string>
```

## Provision with Terraform

You can provision the ClickHouse data source using the [Grafana Terraform provider](https://registry.terraform.io/providers/grafana/grafana/latest/docs). Example with basic authentication:

```hcl
resource "grafana_data_source" "clickhouse" {
  type = "grafana-clickhouse-datasource"
  name = "ClickHouse"

  json_data_encoded = jsonencode({
    host             = "localhost"
    port             = 9000
    protocol         = "native"
    username         = "grafana_reader"
    tlsSkipVerify    = false
    # configMode     = "classic" # or "single-table"
    # signalType     = "logs"    # or "traces"; used when configMode is "single-table"
    # defaultDatabase = "mydb"
    # logs            = {
    #   defaultDatabase = "otel"
    #   defaultTable    = "otel_logs"
    #   otelEnabled     = true
    # }
    # dialTimeout     = "10"
    # queryTimeout    = "60"
    # validateSql     = true
    # enableRowLimit  = true
    # oauthPassThru   = true  # forward the user's OAuth token as a JWT (ClickHouse Cloud only); requires secure = true
  })

  secure_json_data_encoded = jsonencode({
    password = var.clickhouse_password
  })
}
```

For more options and authentication methods, refer to the [Grafana Terraform provider documentation](https://registry.terraform.io/providers/grafana/grafana/latest/docs/resources/data_source).

## Next steps

After configuring the data source:

- [ClickHouse query editor](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/query-editor/) — Build queries with the SQL editor or query builder.
- [ClickHouse template variables](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/template-variables/) — Use variables in dashboards and queries.
- [ClickHouse data source](/docs/plugins/grafana-clickhouse-datasource/<CLICKHOUSE_PLUGIN_VERSION>/) — Overview, supported features, and pre-built dashboards.
