# Graph Report - . (2026-06-25)

## Corpus Check

- 243 files · ~156,048 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary

- 1336 nodes · 2782 edges · 83 communities (75 shown, 8 thin omitted)
- Extraction: 98% EXTRACTED · 2% INFERRED · 0% AMBIGUOUS · INFERRED: 60 edges (avg confidence: 0.81)
- Token cost: 97,336 input · 5,508 output

## Community Hubs (Navigation)

- [[_COMMUNITY_Quota Dashboard|Quota Dashboard]]
- [[_COMMUNITY_Dependencies & Libraries|Dependencies & Libraries]]
- [[_COMMUNITY_Provider Accounts & DB|Provider Accounts & DB]]
- [[_COMMUNITY_Playground & Usage|Playground & Usage]]
- [[_COMMUNITY_File-based Routes|File-based Routes]]
- [[_COMMUNITY_Diagnostics|Diagnostics]]
- [[_COMMUNITY_Venom Models Config|Venom Models Config]]
- [[_COMMUNITY_Antigravity Provider|Antigravity Provider]]
- [[_COMMUNITY_UI Components (shadcn)|UI Components (shadcn)]]
- [[_COMMUNITY_Playground Request Panel|Playground Request Panel]]
- [[_COMMUNITY_Dashboard API Router|Dashboard API Router]]
- [[_COMMUNITY_API Keys Dashboard|API Keys Dashboard]]
- [[_COMMUNITY_UI Hooks & Responsive|UI Hooks & Responsive]]
- [[_COMMUNITY_Models Dashboard|Models Dashboard]]
- [[_COMMUNITY_Routing Config & Auth|Routing Config & Auth]]
- [[_COMMUNITY_Community 15|Community 15]]
- [[_COMMUNITY_Community 16|Community 16]]
- [[_COMMUNITY_Community 17|Community 17]]
- [[_COMMUNITY_Community 18|Community 18]]
- [[_COMMUNITY_Community 19|Community 19]]
- [[_COMMUNITY_Community 20|Community 20]]
- [[_COMMUNITY_Community 21|Community 21]]
- [[_COMMUNITY_Community 22|Community 22]]
- [[_COMMUNITY_Community 23|Community 23]]
- [[_COMMUNITY_Community 24|Community 24]]
- [[_COMMUNITY_Community 25|Community 25]]
- [[_COMMUNITY_Community 26|Community 26]]
- [[_COMMUNITY_Community 27|Community 27]]
- [[_COMMUNITY_Community 28|Community 28]]
- [[_COMMUNITY_Community 29|Community 29]]
- [[_COMMUNITY_Community 30|Community 30]]
- [[_COMMUNITY_Community 31|Community 31]]
- [[_COMMUNITY_Community 32|Community 32]]
- [[_COMMUNITY_Community 33|Community 33]]
- [[_COMMUNITY_Community 34|Community 34]]
- [[_COMMUNITY_Community 35|Community 35]]
- [[_COMMUNITY_Community 36|Community 36]]
- [[_COMMUNITY_Community 37|Community 37]]
- [[_COMMUNITY_Community 38|Community 38]]
- [[_COMMUNITY_Community 39|Community 39]]
- [[_COMMUNITY_Community 40|Community 40]]
- [[_COMMUNITY_Community 41|Community 41]]
- [[_COMMUNITY_Community 42|Community 42]]
- [[_COMMUNITY_Community 43|Community 43]]
- [[_COMMUNITY_Community 44|Community 44]]
- [[_COMMUNITY_Community 45|Community 45]]
- [[_COMMUNITY_Community 46|Community 46]]
- [[_COMMUNITY_Community 47|Community 47]]
- [[_COMMUNITY_Community 48|Community 48]]
- [[_COMMUNITY_Community 49|Community 49]]
- [[_COMMUNITY_Community 50|Community 50]]
- [[_COMMUNITY_Community 51|Community 51]]
- [[_COMMUNITY_Community 52|Community 52]]
- [[_COMMUNITY_Community 53|Community 53]]
- [[_COMMUNITY_Community 54|Community 54]]
- [[_COMMUNITY_Community 55|Community 55]]
- [[_COMMUNITY_Community 56|Community 56]]
- [[_COMMUNITY_Community 57|Community 57]]
- [[_COMMUNITY_Community 58|Community 58]]
- [[_COMMUNITY_Community 59|Community 59]]
- [[_COMMUNITY_Community 60|Community 60]]
- [[_COMMUNITY_Community 61|Community 61]]
- [[_COMMUNITY_Community 62|Community 62]]
- [[_COMMUNITY_Community 63|Community 63]]
- [[_COMMUNITY_Community 64|Community 64]]
- [[_COMMUNITY_Community 65|Community 65]]
- [[_COMMUNITY_Community 66|Community 66]]
- [[_COMMUNITY_Community 67|Community 67]]
- [[_COMMUNITY_Community 68|Community 68]]
- [[_COMMUNITY_Community 69|Community 69]]
- [[_COMMUNITY_Community 70|Community 70]]
- [[_COMMUNITY_Community 72|Community 72]]
- [[_COMMUNITY_Community 73|Community 73]]
- [[_COMMUNITY_Community 74|Community 74]]
- [[_COMMUNITY_Community 75|Community 75]]

## God Nodes (most connected - your core abstractions)

1. `cn()` - 128 edges
2. `fetch()` - 36 edges
3. `handleDashboardAPI()` - 34 edges
4. `Button` - 21 edges
5. `FileRoutesByPath` - 20 edges
6. `Badge()` - 19 edges
7. `api` - 17 edges
8. `compilerOptions` - 17 edges
9. `Header()` - 15 edges
10. `diagnoseAntigravityFetch()` - 14 edges

## Surprising Connections (you probably didn't know these)

- `Venom Router Logo` --references--> `Venom Router Production Design Spec` [INFERRED]
  public/logo.png → docs/superpowers/specs/2026-06-22-venom-router-production-design.md
- `handleFetchModels()` --calls--> `unpackCredentials()` [INFERRED]
  src/lib/api/dashboard-router.server.ts → scripts/fetch-live-models.ts
- `checkAccountModels()` --calls--> `unpackCredentials()` [INFERRED]
  src/lib/db/providers.server.ts → scripts/fetch-live-models.ts
- `runAntigravityLiveSnapshotFetch()` --calls--> `unpackCredentials()` [INFERRED]
  src/lib/providers/integrations.service.ts → scripts/fetch-live-models.ts
- `syncAccountInternal()` --calls--> `unpackCredentials()` [INFERRED]
  src/lib/providers/integrations.service.ts → scripts/fetch-live-models.ts

## Import Cycles

- None detected.

## Communities (83 total, 8 thin omitted)

### Community 0 - "Quota Dashboard"

Cohesion: 0.06
Nodes (49): AccountQuotaCard(), QuotaDashboard(), QuotaExtra, AccountLine(), AccountRow, AntigravityGroupedQuota(), formatRelativeTime(), getPlanBadgeStyles() (+41 more)

### Community 1 - "Dependencies & Libraries"

Cohesion: 0.04
Nodes (56): dependencies, class-variance-authority, clsx, cmdk, date-fns, embla-carousel-react, @fontsource-variable/manrope, @fontsource-variable/sora (+48 more)

### Community 2 - "Provider Accounts & DB"

Cohesion: 0.06
Nodes (39): AccountInfo, AccountModel, AccountQuota, AccountStatus, checkAccountModels(), extractQuotaGroups(), getAccountInfo(), getAccountModels() (+31 more)

### Community 3 - "Playground & Usage"

Cohesion: 0.06
Nodes (35): handleGetUsageAnalytics(), Message, DAY_LABELS, getMetricsSummary(), getTraffic7d(), getUsageAnalytics(), listUsageRecords(), MetricsSummary (+27 more)

### Community 4 - "File-based Routes"

Cohesion: 0.06
Nodes (41): Route, Route, Route, Route, Route, Route, Route, Route (+33 more)

### Community 5 - "Diagnostics"

Cohesion: 0.06
Nodes (32): Route, STAT_ACCENTS, StatCard(), StatusDot(), ChecklistItem(), KpiCard(), Overview(), Route (+24 more)

### Community 6 - "Venom Models Config"

Cohesion: 0.07
Nodes (30): Route, WEIGHT_PRESETS, VenomModel, DebugEntryCard(), PageControls(), PageControlsProps, TabItem, Props (+22 more)

### Community 7 - "Antigravity Provider"

Cohesion: 0.10
Nodes (33): handleLoadAntigravityStoredSnapshot(), AntigravityFetchedModel, AntigravityLiveFetchSnapshot, AntigravityQuotaBucket, AntigravityStoredModelRow, applyTestResultsToSnapshot(), buildAntigravityLiveFetchSnapshot(), buildAntigravityQuotaGroups() (+25 more)

### Community 8 - "UI Components (shadcn)"

Cohesion: 0.08
Nodes (30): cn(), ActionButton(), Breadcrumb, BreadcrumbEllipsis(), BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage (+22 more)

### Community 9 - "Playground Request Panel"

Cohesion: 0.06
Nodes (23): Props, ConnectCredentialDialog(), AccordionContent, AccordionItem, AccordionTrigger, Alert, AlertDescription, AlertTitle (+15 more)

### Community 10 - "Dashboard API Router"

Cohesion: 0.08
Nodes (33): accountIdSchema, buildTraffic7d(), categorySchema, connectCredentialSchema, createKeySchema, createRuleSchema, DAY_LABELS, err() (+25 more)

### Community 11 - "API Keys Dashboard"

Cohesion: 0.11
Nodes (22): ALL_MODELS, DebugContext, KeyRowData, VenomSlug, api, CallbackPayload, OAuthConnectModal(), Step (+14 more)

### Community 12 - "UI Hooks & Responsive"

Cohesion: 0.06
Nodes (29): useIsMobile(), Separator, SheetDescription, Sidebar, SidebarContent, SidebarContext, SidebarContextProps, SidebarFooter (+21 more)

### Community 13 - "Models Dashboard"

Cohesion: 0.10
Nodes (14): KpiMini(), CatalogModel, CAPABILITY_META, ModelCapabilityIcons(), AccountModel, Checkbox, SelectContent, SelectItem (+6 more)

### Community 14 - "Routing Config & Auth"

Cohesion: 0.13
Nodes (16): TIERS, RoutingDebugContext, RoutingDebugController, RoutingTierSection(), RuleCard(), AddRuleSheet(), TierStrategyForm(), Card (+8 more)

### Community 15 - "Community 15"

Cohesion: 0.16
Nodes (21): chat(), ClaudeAuthError, ClaudeProfile, completeFlow(), fetchIdentity(), fetchProfileEndpoint(), fetchUserinfoFallback(), formatPlan() (+13 more)

### Community 16 - "Community 16"

Cohesion: 0.16
Nodes (18): handlePlaygroundChat(), CapabilityFilter(), ACCOUNT_ROTATION_OPTIONS, ApprovedModel, AUTO_ESCALATION_OPTIONS, CAPABILITY_FILTER_OPTIONS, CAPABILITY_LABELS, FALLBACK_BEHAVIOR_OPTIONS (+10 more)

### Community 17 - "Community 17"

Cohesion: 0.15
Nodes (21): handleCompleteOAuthFlow(), handleConnectCredential(), handleFetchModels(), handleListCatalogModels(), isEligibleForRouting(), aggregateCatalogModels(), CatalogRowInput, extractCapabilityList() (+13 more)

### Community 18 - "Community 18"

Cohesion: 0.13
Nodes (19): AccountHealthResult, AntigravityClientCreds, AntigravityProfile, b64urlChallenge(), buildIdentityFromSync(), COMMON_HEADERS, completeFlow(), fetchIdentity() (+11 more)

### Community 19 - "Community 19"

Cohesion: 0.11
Nodes (21): src/lib/providers/adapters/antigravity.server.ts, src/lib/api-client.ts, src/lib/providers/adapters/claude-code.server.ts, src/lib/crypto.server.ts, src/lib/api/dashboard-router.server.ts, src/lib/workers/health-check.server.ts, src/lib/providers/adapters/opencode-zen.server.ts, src/lib/workers/quota-snapshot.server.ts (+13 more)

### Community 20 - "Community 20"

Cohesion: 0.15
Nodes (17): buildDiagnosisConclusions(), diagnoseAntigravityFetch(), suspiciousIds(), buildSearchReport(), ENTRY_FIELDS, extractAgentModelSortIds(), findModelMapCandidates(), findStringsInRawResponse() (+9 more)

### Community 21 - "Community 21"

Cohesion: 0.10
Nodes (19): compilerOptions, allowImportingTsExtensions, jsx, lib, module, moduleResolution, noEmit, noFallthroughCasesInSwitch (+11 more)

### Community 22 - "Community 22"

Cohesion: 0.16
Nodes (14): AccountHealthResult, chat(), checkAccountHealth(), fetchIdentity(), fetchModelsDevCatalog(), fetchZenModelsRaw(), listModels(), syncOpenCodeZenAccount() (+6 more)

### Community 23 - "Community 23"

Cohesion: 0.11
Nodes (14): Logo(), LogoMark(), LogoMarkProps, LogoProps, NAV_INSIGHTS, NAV_MANAGE, NAV_OPERATE, NAV_PRIMARY (+6 more)

### Community 24 - "Community 24"

Cohesion: 0.11
Nodes (18): aliases, components, hooks, lib, ui, utils, iconLibrary, registries (+10 more)

### Community 25 - "Community 25"

Cohesion: 0.21
Nodes (13): avgCostPerMtok(), enrichCandidate(), ESCALATION_STAGES, EscalationStage, getCostType(), getEscalationStages(), getQualityScore(), isPremium() (+5 more)

### Community 26 - "Community 26"

Cohesion: 0.17
Nodes (16): bearerHeaders(), chat(), checkAccountHealth(), fetchGoogleUserinfo(), loadCodeAssist(), onboardUser(), refreshIfNeeded(), testModel() (+8 more)

### Community 27 - "Community 27"

Cohesion: 0.14
Nodes (16): handleListAccountModels(), AntigravityLiveModelEntry, mergeDbOverlay(), AccountModelJoinRow, CatalogRow, mapJoinToAccountModelView(), mapJoinToCatalogRow(), unwrap() (+8 more)

### Community 28 - "Community 28"

Cohesion: 0.11
Nodes (18): devDependencies, eslint, eslint-config-prettier, @eslint/js, eslint-plugin-prettier, eslint-plugin-react-hooks, eslint-plugin-react-refresh, globals (+10 more)

### Community 29 - "Community 29"

Cohesion: 0.20
Nodes (13): errorBody(), handleChatCompletions(), json(), MODEL_MAP, ApiKeyError, checkKeyLimits(), KeyLimitResult, startOfUtcDay() (+5 more)

### Community 30 - "Community 30"

Cohesion: 0.20
Nodes (13): handleDisconnectAccount(), inferQualityRating(), resolveModelSpecs(), AccountLinkRow, buildCapabilities(), catalogFromJoin(), CatalogRow, deleteModelIfOrphaned() (+5 more)

### Community 31 - "Community 31"

Cohesion: 0.15
Nodes (13): getVenomModel(), listRoutingRules(), listVenomModels(), RoutingRule, AutoEscalation, FallbackBehavior, HealthRequirement, TIER_SCORING_WEIGHTS (+5 more)

### Community 32 - "Community 32"

Cohesion: 0.12
Nodes (11): Menubar, MenubarCheckboxItem, MenubarContent, MenubarItem, MenubarLabel, MenubarRadioItem, MenubarSeparator, MenubarShortcut() (+3 more)

### Community 33 - "Community 33"

Cohesion: 0.16
Nodes (10): Route, Route, HeaderProps, ThemeToggle(), Theme, ThemeContext, ThemeCtx, ThemeProvider() (+2 more)

### Community 34 - "Community 34"

Cohesion: 0.17
Nodes (9): log, reportClientError(), serializeError(), ErrorComponent(), Route, ThemedToaster(), FileRoutesById, Toaster() (+1 more)

### Community 35 - "Community 35"

Cohesion: 0.19
Nodes (11): Venom Router Logo, Venom Router Production Design Spec, applyAccountRotation(), interleaveByAccount(), remainingQuotaFraction(), AccountRotation, Modality, RoutingRequest (+3 more)

### Community 36 - "Community 36"

Cohesion: 0.14
Nodes (11): FormControl, FormDescription, FormFieldContext, FormFieldContextValue, FormItem, FormItemContext, FormItemContextValue, FormLabel (+3 more)

### Community 37 - "Community 37"

Cohesion: 0.14
Nodes (12): Carousel, CarouselApi, CarouselContent, CarouselContext, CarouselContextProps, CarouselItem, CarouselNext, CarouselOptions (+4 more)

### Community 38 - "Community 38"

Cohesion: 0.26
Nodes (11): loadCodeAssistBody(), OAUTH_CLIENT_METADATA, LiveModelEntry, antigravityHeaders(), AntigravityModelQuota, AntigravityUsageSnapshot, buildAntigravityUsageQuotas(), fetchAntigravitySubscription() (+3 more)

### Community 39 - "Community 39"

Cohesion: 0.18
Nodes (10): requireDashboardAuth(), CompositeTypes, Constants, Database, DatabaseWithoutInternals, DefaultSchema, Enums, Tables (+2 more)

### Community 40 - "Community 40"

Cohesion: 0.17
Nodes (12): scripts, build, build:dev, dev, format, lint, preview, test (+4 more)

### Community 41 - "Community 41"

Cohesion: 0.26
Nodes (9): buildVisibleCatalogModels(), extractIdsFromGroups(), extractRecommendedModelIds(), AntigravityUpsertModelInput, buildIdeVisibleUpsertInput(), liveModelsFromRaw(), rawCatalogCount(), RECOMMENDED_IDS (+1 more)

### Community 42 - "Community 42"

Cohesion: 0.29
Nodes (10): buildTraceCandidates(), routeRequest(), toTraceCandidate(), detectModality(), mergeStrategyConfig(), log, PersistOpts, persistUsageAndTrace() (+2 more)

### Community 43 - "Community 43"

Cohesion: 0.25
Nodes (10): fetchAntigravityLiveRaw(), fetchProfile(), needsProjectResolution(), buildAntigravityPlanInfo(), formatAntigravityPlan(), isFreeTierLabel(), LoadCodeAssistResponse, resolveAntigravityPlan() (+2 more)

### Community 44 - "Community 44"

Cohesion: 0.24
Nodes (9): listModels(), ANTIGRAVITY_FETCH_ENDPOINT_VARIANTS, antigravityHeaders(), fetchAntigravitySnapshot(), fetchAvailableModels(), FetchAvailableModelsBodyVariant, fetchAvailableModelsRaw(), FetchAvailableModelsRawResult (+1 more)

### Community 45 - "Community 45"

Cohesion: 0.24
Nodes (7): Route, SettingsPage(), DebugEntry, AuthState, useAuth(), log, supabase

### Community 46 - "Community 46"

Cohesion: 0.36
Nodes (8): filterCandidates(), filterCandidatesWithDiagnostics(), FilterDiagnostics, getFilterReason(), isPremiumReserved(), isQuotaExhausted(), matchesCondition(), RoutingCandidate

### Community 47 - "Community 47"

Cohesion: 0.18
Nodes (7): ChartConfig, ChartContainer, ChartContext, ChartContextProps, ChartLegendContent, ChartTooltipContent, THEMES

### Community 48 - "Community 48"

Cohesion: 0.18
Nodes (9): Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList, CommandSeparator, CommandShortcut() (+1 more)

### Community 49 - "Community 49"

Cohesion: 0.29
Nodes (9): AntigravityLiveRawResult, SyncAntigravityResult, SyncOpenCodeZenResult, AccountIdentity, ChatResult, DiscoveredModel, ModelTestResult, StoredCredentials (+1 more)

### Community 50 - "Community 50"

Cohesion: 0.20
Nodes (9): ContextMenuCheckboxItem, ContextMenuContent, ContextMenuItem, ContextMenuLabel, ContextMenuRadioItem, ContextMenuSeparator, ContextMenuShortcut(), ContextMenuSubContent (+1 more)

### Community 51 - "Community 51"

Cohesion: 0.33
Nodes (5): ChatMessage, classifyTask(), extractTextContent(), hasImageContent(), TaskClass

### Community 52 - "Community 52"

Cohesion: 0.25
Nodes (7): AccountRow, **dirname, fetchAvailableModels(), **filename, main(), StoredCredentials, supabase

### Community 53 - "Community 53"

Cohesion: 0.36
Nodes (8): ClaudeQuotaWindow, ClaudeUsageResult, createQuotaObject(), fetchClaudeUsage(), getClaudeUsageLegacy(), hasUtilization(), oauthCooldown, oauthHeaders()

### Community 54 - "Community 54"

Cohesion: 0.22
Nodes (8): Table, TableBody, TableCaption, TableCell, TableFooter, TableHead, TableHeader, TableRow

### Community 55 - "Community 55"

Cohesion: 0.39
Nodes (6): isCatalogEntryAvailable(), isZeroCost(), OpenCodeZenCatalogEntry, OpenCodeZenFetchedModel, OpenCodeZenModelCost, catalog

### Community 56 - "Community 56"

Cohesion: 0.25
Nodes (7): NavigationMenu, NavigationMenuContent, NavigationMenuIndicator, NavigationMenuList, NavigationMenuTrigger, navigationMenuTriggerStyle, NavigationMenuViewport

### Community 57 - "Community 57"

Cohesion: 0.29
Nodes (7): API Keys DB Service, Internal DB API Design Spec, Internal DB API Implementation Plan, Providers DB Service, Providers DB Service Tests, Usage DB Service, Venom DB Service

### Community 58 - "Community 58"

Cohesion: 0.33
Nodes (4): Route, Sidebar(), DashboardChrome, DashboardChromeContext

### Community 59 - "Community 59"

Cohesion: 0.38
Nodes (4): ApiKey, getApiKey(), listApiKeys(), SAMPLE_KEY

### Community 60 - "Community 60"

Cohesion: 0.33
Nodes (5): ToggleGroup, ToggleGroupContext, ToggleGroupItem, Toggle, toggleVariants

### Community 61 - "Community 61"

Cohesion: 0.47
Nodes (5): decryptSecret(), encryptSecret(), generateApiKey(), getKey(), hashApiKey()

### Community 62 - "Community 62"

Cohesion: 0.53
Nodes (5): Do-Start(), Do-Stop(), Ensure-WorkerScript(), Find-ServerPid(), Test-Up()

### Community 63 - "Community 63"

Cohesion: 0.50
Nodes (4): ChatRequest, executeWithFallback(), ExecutionResult, getAdapterChat()

### Community 65 - "Community 65"

Cohesion: 0.40
Nodes (4): name, private, sideEffects, type

### Community 66 - "Community 66"

Cohesion: 0.60
Nodes (4): Do-Start(), Do-Stop(), Find-ServerPid(), Test-Up()

### Community 67 - "Community 67"

Cohesion: 0.50
Nodes (4): Task Classifier, Routing Policy, Quota-Aware Routing Policy Implementation Plan, Strategy Types

## Knowledge Gaps

- **512 isolated node(s):** `$schema`, `style`, `rsc`, `tsx`, `css` (+507 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **8 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions

_Questions this graph is uniquely positioned to answer:_

- **Why does `cn()` connect `UI Components (shadcn)` to `Quota Dashboard`, `Playground & Usage`, `Diagnostics`, `Venom Models Config`, `Playground Request Panel`, `API Keys Dashboard`, `UI Hooks & Responsive`, `Models Dashboard`, `Routing Config & Auth`, `Community 16`, `Community 23`, `Community 32`, `Community 33`, `Community 36`, `Community 37`, `Community 47`, `Community 48`, `Community 50`, `Community 54`, `Community 56`, `Community 58`, `Community 60`?**
  _High betweenness centrality (0.169) - this node is a cross-community bridge._
- **Why does `fetch()` connect `Community 26` to `Provider Accounts & DB`, `Community 38`, `Dashboard API Router`, `Community 44`, `Community 15`, `Community 18`, `Community 52`, `Community 53`, `Community 22`, `Community 29`?**
  _High betweenness centrality (0.044) - this node is a cross-community bridge._
- **Why does `handleDashboardAPI()` connect `Dashboard API Router` to `Playground & Usage`, `Antigravity Provider`, `Community 39`, `Community 59`, `Community 16`, `Community 17`, `Community 26`, `Community 27`, `Community 61`, `Community 30`, `Community 31`?**
  _High betweenness centrality (0.036) - this node is a cross-community bridge._
- **Are the 28 inferred relationships involving `fetch()` (e.g. with `chat()` and `completeFlow()`) actually correct?**
  _`fetch()` has 28 INFERRED edges - model-reasoned connections that need verification._
- **Are the 8 inferred relationships involving `handleDashboardAPI()` (e.g. with `listApiKeys()` and `listRoutingRules()`) actually correct?**
  _`handleDashboardAPI()` has 8 INFERRED edges - model-reasoned connections that need verification._
- **What connects `$schema`, `style`, `rsc` to the rest of the system?**
  _512 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Quota Dashboard` be split into smaller, more focused modules?**
  _Cohesion score 0.060655737704918035 - nodes in this community are weakly interconnected._
