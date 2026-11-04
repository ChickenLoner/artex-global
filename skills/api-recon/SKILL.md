---
name: api-recon
description: Use this skill when collecting a website's API endpoints.
---

# API Recon (Frontend API Reconnaissance)

Under **authorized** conditions, discover as completely as possible: the **backend APIs** (path, method, parameters, response body), the **frontend routes**, and the **UI feature trigger points** (tabs, dialogs, table actions, etc.).

---

## Boundaries and Prohibitions (Agents must read; violations are out of bounds)

This skill **only performs API / parameter-surface reconnaissance**. It is not a vulnerability-hunting or exploitation phase.

### Task boundaries

| Scope | Allowed | Prohibited |
|---|---|---|
| **Target** | Enumerate path, method, parameters, routes, UI trigger points | SQLi/XSS/privilege escalation/brute force/fuzzing for vulnerabilities, packet-tampering attacks, destructive operations |
| **Authentication** | Hook + stub/mock to bypass the **client-side** login gate | Asking the user for, or guessing, account credentials; attempting a real login form submission |
| **Runtime** | Hook APIs without credentials and use mock responses so the SPA enters the post-login shell | Flows that depend on a real backend session to continue |

### Credential-free dynamic analysis (Phase 3 default)

1. Use `preload.js` / `runtime_harvest.js` to **intercept and stub** bootstrap APIs such as login, permissions, and menus;
2. For business query APIs, return a mock body that is **structurally correct, has a success business code, and may contain empty data**;
3. Let the frontend render post-login pages even with no backend or in a 401 environment, so as to trigger more XHR/fetch/WebSocket traffic;
4. **Empty data, blank tables, and placeholder UI are all expected**; do not switch to a real login or vulnerability testing because of them.

**In one sentence**: use mocks to prop open frontend routes and component mounting, and **record only outbound requests**; what the backend returns does not matter, what matters is **which further APIs** the frontend will call.

### Hard process prohibitions

| Prohibited | Do this instead |
|---|---|
| Before Phase 1 is complete, grep/curl/Read the main entry `index-*.js` to extract API paths | Run `OUTDIR/harvest_static.py` |
| Hand-writing scripts such as `extract_apis.py` to replace harvest | Edit `OUTDIR/harvest_static.py` and rerun |
| Repeating the same grep/command after it has failed 2 or more times | Change strategy: read tool_logs, edit harvest, consult reference |
| Skipping gate A/B and running the original `scripts/` directly | Copy to OUTDIR and adapt to the target |
| Real usernames/passwords, OTP, OAuth, or other authentication | stub/mock (see above) |
| Skipping stubs on the pretext of "getting real data" and doing privilege-escalation/injection tests | Record outbound only; that is the recon boundary |
| Deleting, exporting sensitive data, bulk writes, or other irreversible operations | The same applies to coverage clicks |
| Claiming all pages and APIs have been obtained without completing runtime + dynamic enumeration | See "Definition of Done" or note the limitations |
| Claiming all parameters are known without completing the parameter trigger matrix + diff | Phase 3b matrix + Phase 5 diff |
| Inferring required/optional from a single runtime sample | Multi-sample diff, or reverse-infer from validation rules/errors |

---

## Two-Layer Model + Run Modes

| Layer | Output | Ceiling |
|---|---|---|
| **Static** (JS bundle) | Full endpoint paths, route draft, payload-assembly field candidates | No HTTP method; parameters need Phase 1b; misses URLs assembled at runtime |
| **Runtime** (live session) | Method + body + response + dynamic URLs + WS/SSE; multi-sample diff fills in parameters | Requests are only sent if the page actually renders; one sample is not enough to decide required/optional |

| Run mode | Engine | Suited for |
|---|---|---|
| **depth** | `runtime_harvest.js` (Puppeteer) | API inventory, METHOD/params/response bodies, WS/SSE, reproducible batch runs |
| **coverage** | browser + `preload.js` | Clicking tabs/dialogs/tables, deeper feature-point coverage |
| **both** | depth first, then coverage | Most complete, takes longest |

**Parameter methodology** (no universal script): use harvest/regex for paths; for parameters use **anchor window expansion + UI binding chain + multi-sample diff + error reverse-inference** (grep recipes are in section J of [reference.md](reference.md)).

---

## Definition of Done

Only when all of the following are met may recon be declared complete:

- [ ] **Static**: Phase 1 harvest produced `api_static.txt`, `routes.txt`, `js/`
- [ ] **Runtime**: at least one of depth or coverage; coverage/both must have **Hook effective + dynamic enumeration loop**
- [ ] **Reached the shell**: visiting business paths does not land on `/login` (mind hash routing)
- [ ] **Parameters**: coverage/both completed the parameter trigger matrix + `param_samples.json`; Phase 5 merged into `params_merged.json`
- [ ] **Depth** (if module pages are blank): Phase 4 permission tree reconstructed and rerun, until **module-level APIs** appear (not just locale/bootstrap)
- [ ] **Delivery**: Phase 5 outputs complete (see the Phase 5 outputs table); `insert_assets` has written service and endpoint assets

---

## Scripts and Gates

`scripts/` contains reference templates only; it is **prohibited** to run the originals directly and treat the result as final.

**Rule**: read first -> adapt to the target -> write into `OUTDIR` (e.g. `recon/`) -> record in `CHANGES.md`; if it does not match, rewrite per the methodology and borrow only the structure.

| Gate | When | Reference script -> OUTDIR copy | Common required changes |
|---|---|---|---|
| **A (static)** | After Phase 0, before the **first** run of harvest/spider | `harvest_static.py` / `spider_mpa.py` | **The default regex runs as-is on most sites**; change the endpoint regex, webpack/Vite `publicPath`, MPA exclude/cookie only when the manifest/dialect does not match |
| **B (runtime)** | After Phase 2, before running depth/coverage | `runtime_harvest.js` / `preload.js` + `config.json` | Cookie/localStorage keys, neutralize success value, stubs, login regex, api prefix, hash/history |

**SPA mandatory order** (not interchangeable; phase numbering takes precedence over "explore first, script later"):

| Step | Must | Prohibited |
|---|---|---|
| After Phase 0 completes | The next Bash = `python3 OUTDIR/harvest_static.py <URL> OUTDIR` | curl/grep/Read the main entry `index-*.js` (usually >500KB) |
| Gate A | Copy script -> small edits if needed -> **run immediately** | Manually extracting APIs first and then deciding whether to harvest |
| Before Phase 1 completes | Verify output with `wc -l`; on 404, edit harvest and retry | Hand-writing extract scripts; repeatedly grepping URLs that were not downloaded |
| From Phase 1b | grep only `OUTDIR/js/*.js` | Using the main bundle in place of harvest |

- ✅ Copy `harvest_static.py` -> (optionally) edit the regex -> **run immediately**
- ❌ curl the main bundle -> grep several times -> write a temporary extract -> only then harvest
- **MPA**: after Phase 0, the next Bash = `python3 OUTDIR/spider_mpa.py ...`

---

## Tool and Output Constraints

| Constraint | Description |
|---|---|
| Large files | `index-*.js` files >100KB are **prohibited** from being Read/grepped into context; process them in batch with OUTDIR scripts |
| grep output | Must use `\| head -20` or `-m 5`; keep only a path summary in the conversation, do not paste bundle snippets |
| Verification | Use `wc -l`, `ls \| wc -l`; do not Read an entire directory |
| Regex probing | Optional, at most 1 time, only on small chunks <=50KB or HTML; for the formal static result, harvest is authoritative |
| reference | For recipes/templates/troubleshooting see [reference.md](reference.md); do not repeat its full text inline |

---

## Execution Roadmap

```
Phase 0 classification + OUTDIR
  → Gate A → Phase 1 harvest (★ run immediately ★)
  → Phase 1b parameter reverse engineering
  → Phase 2 three auth gates → config.json
  → Gate B → Phase 3 runtime + parameter matrix
  → Phase 4 permission tree (when necessary) → rerun Phase 3
  → Phase 5 merge report + insert_assets batch-inserts all discovered service and endpoint API assets; under no circumstances may any discovered asset be left out of the insert
```

Check items off in order; **do not enter the next Phase until the previous one is complete**.

1. [ ] **Phase 0**: probe SPA/MPA; create `OUTDIR` -> [Phase 0](#phase-0--classification)
2. [ ] **Gate A + Phase 1**: copy scripts -> harvest **immediately** -> verify with `wc -l` -> [Phase 1](#phase-1--static)
3. [ ] **Phase 1b**: anchor window expansion + binding layer -> `param_candidates.json` -> [Phase 1b](#phase-1b--parameter-reverse-engineering)
4. [ ] **Phase 2**: three auth gates -> `config.json` -> [Phase 2](#phase-2--the-three-auth-gates)
5. [ ] **Gate B**: adjust runtime scripts -> [Phase 3](#phase-3--runtime)
6. [ ] **Phase 3**: depth / coverage / both; confirm reaching the shell; parameter trigger matrix -> `param_samples.json`
7. [ ] **Phase 4** (if needed): permission tree -> patch stubs -> rerun Phase 3 -> [Phase 4](#phase-4--permission-tree-reconstruction)
8. [ ] **Phase 5**: merged outputs + report + `insert_assets` -> [Phase 5](#phase-5--merge-and-report)

---

## Phase 0 — Classification

Fetch the entry HTML and **create `OUTDIR`** (do not modify `scripts/` inside the skill):

- **SPA**: empty shell + `<div id=app>` + chunks -> Phase 1–5
- **MPA**: SSR + `<form>`, no endpoint bundle -> after Gate A:

```bash
python3 recon/spider_mpa.py <BASE_URL> <OUTDIR> [--cookie "session=..."] [--max 300] [--depth 5] [--exclude "logout|delete|destroy"]
```

Outputs `forms.txt`, `links.txt`, `api_inline.txt`. If an SPA has forms ≈ 0 -> switch to Phase 1.

---

## Phase 1 — Static

Follow [Scripts and Gates](#scripts-and-gates) · [Tool and Output Constraints](#tool-and-output-constraints).

```bash
python3 recon/harvest_static.py <BASE_URL> <OUTDIR>
```

harvest: parses the HTML scripts -> webpack/Vite manifest -> downloads all lazy chunks -> produces `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt`.

```bash
wc -l OUTDIR/api_static.txt OUTDIR/routes.txt
ls OUTDIR/js | wc -l
```

- Chunk count vs manifest: on 404, edit harvest and retry; do not curl chunks one by one by hand
- If `api_static.txt` is too small -> loosen the endpoint regex inside OUTDIR and rerun (see reference)

### Phase 1b — Parameter Reverse Engineering

Paths come from Phase 1; parameter fields need separate recon. For grep rules see [Tool and Output Constraints](#tool-and-output-constraints).

**Completion criterion**: for the important APIs you can answer: field name, transport location, inferred type, whether required, sample value, confidence.

#### 1b.0 — Transport form

| Form | Where the parameters are | What to look at first statically |
|---|---|---|
| REST JSON | body + query | `(params\|data\|body)\s*:\s*\{` next to the path anchor |
| GraphQL | `variables` | gql templates, `$page: Int` |
| Traditional form | urlencoded | `<form>`, `FormData` |
| File upload | multipart | `FormData.append` |
| Path parameters | `/user/:id` | route table + `useParams` / `$route.params` |
| Encrypted/signed | wrapped into `sign`/`data` | Hook the encryption function's arguments (reference section D) |

Output: annotate each API with `transport: query|json|form|graphql|encrypted`.

#### 1b.1 — Anchor window expansion

Using a known path as the anchor, widen the window to find the payload-assembly object:

```bash
grep -n '"/api/user/list"' OUTDIR/js/*.js | head -20
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' OUTDIR/js/*.js | head -20
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' OUTDIR/js/*.js | head -20
```

| Wrapper layer | Parameter clue |
|---|---|
| axios instance | `data` / `params` |
| Unified request | Interceptor-injected global fields |
| OpenAPI client | Generated method signature |
| React Query / SWR | The hook's second argument |
| Vue composable | The composable's arguments |

Type remnants: `yup`/`zod`/rules, `Form.Item name=`, embedded Swagger.

-> `param_candidates.json`: `{ path, fields[], source: "static-callsite", confidence }`

#### 1b.2 — Binding layer

```
Form field → onFinish/handleSubmit → transform → API payload
```

| Binding source | Technique |
|---|---|
| Form submit | Follow submit -> transform -> API |
| Table search | `getFieldsValue()` -> `params` |
| Route | `:id` / `?tab=` |
| Interceptor | Global `tenantId`, pagination, sign |
| Enum select | `options` -> API enum values |

Trace upward through the DevTools call stack from `fetch`/`XHR.send` to the payload-assembly function.

#### 1b.3 — The three payload questions (≠ Phase 2's three auth gates)

| Question | What to answer |
|---|---|
| **Assembly** | Where the payload is built, traces of transforms |
| **Validation** | required, pattern, enum |
| **Transport** | path / query / body / multipart / headers |

The interceptor gate (Phase 2) incidentally reads globally injected fields (Authorization, `X-Tenant-Id`, sign).

#### 1b.4 — Handoff to Phase 3

Candidate fields come from the static/binding layers; **required/optional/conditional dependencies** need the Phase 3 parameter matrix + diff + Phase 5 error reverse-inference.

---

## Phase 2 — The Three Auth Gates

grep in `OUTDIR/js/` (with `head`) and write into `config.json` (recipes in reference):

| Gate | Question | Keywords |
|---|---|---|
| **Render gate** | How is "logged in" determined? | `isLogin`, `getToken`, Cookie/localStorage |
| **Interceptor gate** | What triggers a redirect to `/login`? | `response_code`, `errno`, axios interceptor |
| **Content gate** | Where do menus/permissions come from? | `menu`, `permission`, `role`, `acl`, `routes` |

Do not treat a localStorage key name as a credential; it must be confirmed from the chunk/request chain.

**Exit = Gate B**: land the conclusions in `config.json`, and modify `OUTDIR/runtime_harvest.js` / `preload.js`.

### Phase 2b — API observation (optional)

Use the `preload.js` in OUTDIR to confirm session key names, Authorization, and nested API URLs:

| Config | Output |
|---|---|
| `recordDetail: true` | `__API_RECON_DETAIL__` |
| `observe.xhrHeaders: true` | header observation |
| `extractUrlsFromResponse: true` | sub-APIs inside responses |
| `observe.storageReads/cookieReads: true` | backfill config |
| `neutralizeVueRouter: true` | `__API_RECON_ROUTES__` |

Export each coverage round: `__API_RECON_LOG__`, `__API_RECON_DETAIL__`, `__API_RECON_ROUTES__`, `__API_RECON_OBSERVE__`.

---

## Phase 3 — Runtime

Gate B must already be passed; follow [Boundaries and Prohibitions](#boundaries-and-prohibitions-agents-must-read-violations-are-out-of-bounds) · the credential-free mock strategy.

Set `"runtimeMode": "depth" | "coverage" | "both"` in `config.json` (template in reference).

### Hook and stub (shared by depth + coverage)

| Layer | Scope | Purpose |
|---|---|---|
| L1 exact | auth/permission/bootstrap stubs | Pass first-screen authentication |
| L2 negative correction | All JSON responses | Not-logged-in code -> success |
| L3 fallback | `/api` etc. not matched by L1 | Empty success body, to prop up the UI |

- **depth**: fake auth + `forward` rewriting business codes + `stubs`; traverse `routes` (hash/history); output `runtime_api.json`
- **coverage**: inject `preload.js` at **document-start** (CDP `addScriptToEvaluateOnNewDocument` or a Userscript)

Verify: `window.__API_RECON_PRELOAD__` exists; business paths do not return to `/login`.

```bash
cd recon && npm install
node runtime_harvest.js config.json
```

### 3b — coverage dynamic enumeration (required)

1. Main navigation/sidebar — click each item, wait 1–3s for the network
2. Tabs — `role=tab`, `.ant-tabs-tab`
3. Tables — first row view/edit/detail
4. Toolbar — export, filter, create (**avoid irreversible deletes**)
5. On entering each module — merge APIs/routes
6. SPA — controlled `pushState` to paths not covered by `routes.txt` (prohibited for MPA)

**Parameter trigger matrix** (required): per module, record once per operation type, and **diff the multiple samples**:

| Operation | Parameters usually added |
|---|---|
| List first screen | Pagination + default filters |
| Click search | keyword, filter |
| Advanced filter | More optional fields |
| Create/edit | Complete entity |
| Bulk/export/sort | `ids[]`, `exportType`, `sortField` |

**Under stubs the outbound body/headers are still real**; the request is authoritative. Recording -> `scan_raw.json`, `param_samples.json`, `api_detail.json`.

- **Vue**: `neutralizeVueRouter: true` + document-start preload
- **React**: `routes.txt` + sidebar clicks + `pushState`
- **both**: 3a depth first, then 3b coverage

---

## Phase 4 — Permission Tree Reconstruction

**Trigger**: module pages are blank / each route only has bootstrap (e.g. locale) -> the content gate was not passed.

| Symptom | Meaning |
|---|---|
| Reached the shell successfully | Render gate + interceptor gate passed |
| Sidebar items missing / clicking shows blank | stub shape or permission codes incomplete |
| Every route has the same, very few APIs | `v-if permission` not passed |
| `routes.txt` far smaller than the bundle | Must be completed from the auth module |

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl)[^"]*"' OUTDIR/js/*.js | sort -u | head -30
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|menuList|authList' OUTDIR/js/*.js | head -20
```

Typical chain: `role_permissions` (flat codes) + `permissions/all` (tree) -> `getResultTree` -> `userRouteAuth[CODE].url`.

```bash
python3 recon/extract_route_map.py recon/js recon/
python3 recon/build_perm_tree.py recon/js recon/ --config recon/config.json
```

Intermediate outputs: `route_map.json`, `userRouteAuth.json`, `permissions_tree.json`, `*_stub.json`, `perm_codes_all.txt`.

Stub checks: the outer `response_code` matches the interceptor gate; flat codes align with the tree; `routes` covers all links in `route_map`.

After updating `config.json`, **rerun Phase 3**. For large SPAs you can tune `waitUntil`, `routeTimeout`, `perRouteMs` (see reference sections A3/I).

---

## Phase 5 — Merge and Report

### Outputs table

| File | Phase | Content |
|---|---|---|
| `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt` | 1 | Static bundles and paths |
| `param_candidates.json` | 1b | Static parameter field candidates |
| `config.json` | 2 | Three gates + runtime config |
| `runtime_api.json` | 3a | depth detailed recording (including WS/SSE) |
| `param_samples.json`, `scan_raw.json`, `api_detail.json` | 3b | Multiple samples, click log, detail |
| `route_map.json` etc. | 4 | Permission tree intermediates (if performed) |
| `params_merged.json` | 5 | Merged parameter fields + confidence |
| `api_merged.txt` | 5 | `METHOD /path [params] [static\|runtime\|both]` |
| `site_map.json` | 5 | Routes, APIs, params, feature points, limitations |
| **insert_assets** | 5 | Write all service and endpoint assets into the asset store |

### 5b — Parameter merging

Diff from `param_samples.json`; there is **no universal merge script**. For the confidence rules see reference J7 (high/medium/low/pending-trigger).

### 5c — Error reverse-inference

Within the authorized scope you may send incomplete requests to read 400s (**this is parameter recon, not vulnerability testing**): `field 'x' is required`, enum errors, etc. Watch out for the `data` wrapper, `variables`, and the pre-encryption `bizData`.

The report must state: runtimeMode, the static/runtime API counts, parameter confidence, uncovered modules, and a summary of `CHANGES.md` relative to the reference scripts.

Suggested structure of `site_map.json`:

```json
{
  "site": "https://example.com",
  "runtimeMode": "both",
  "appType": "vue-spa",
  "routeGuardStrategy": ["nav-neutralize", "L1-auth", "L2-patch", "forward"],
  "apisFromStatic": [],
  "apisFromRuntime": [],
  "apis": [],
  "params": [{ "method": "POST", "path": "/api/user/list", "transport": "json", "fields": [] }],
  "frontendRoutes": [],
  "routesVerifiedByClick": [],
  "featuresTriggered": [],
  "limitations": ""
}
```

For more fields and grep recipes see [reference.md](reference.md).

---

## General Notes

- **Framework-agnostic**: the method is the same for webpack/Vite/Angular lazy loading
- **Transport**: REST/JSON, GraphQL, WebSocket, SSE; gRPC-web is out of scope
- **SSR**: client-side fetch can be recorded; RSC/Server Actions are not fully enumerable
- **Blind spots**: JSVMP, WASM, HMAC/mTLS strong validation -> static + note the limitation
- **Parameter blind spots**: conditional linkage, hidden params, WASM payload assembly -> "pending trigger" / "unreachable"
- **Static is the safety net**: even when runtime is blocked, static can still enumerate endpoints

---

## Additional Resources

- Grep recipes, `config.json` template, troubleshooting, Hooks, parameter reverse-engineering section J, site_map template: **[reference.md](reference.md)**
- For reference script paths see the [Scripts and Gates](#scripts-and-gates) table
