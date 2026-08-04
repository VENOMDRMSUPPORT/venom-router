# تقرير تسليم عاجل — ClinePass وLive Models

التاريخ: 2026-08-04  
المشروع: C:\Users\hamee\Desktop\venom-router  
الفرع الحالي: main  
سطح التطوير المطلوب اختباره: http://127.0.0.1:8088  
الخلفية المشتركة الحالية: http://127.0.0.1:8081  
الحالة: تنفيذ غير مكتمل وغير مقبول بصريًا، ولا يجوز تقديمه على أنه منتهٍ.

## 1. الغرض من هذا الملف

هذا تقرير تسليم لوكيل جديد حتى لا يضطر المالك إلى إعادة شرح الجلسة. يوضح:

- قواعد العمل التي يجب الالتزام بها.
- المطلوب الوظيفي الحقيقي.
- مرجع ClinePass الذي استُخرج من التطبيق القديم.
- حالة الحسابين الحالية.
- ما كان موجودًا من محاولة Claude وما غيّره Codex.
- ما نجح باختبارات مركزة.
- ما لم يُثبت أو لم يكتمل.
- الأخطاء البصرية والمعمارية الحالية التي تسبب غضب المالك.
- خطة تصحيح دقيقة للوكيل التالي بدون ترقيعات.

مهم: لا تثق في أي ادعاء داخل هذا الملف دون التحقق من الكود والنظام الحي. الـ worktree مختلط ويحتوي تعديلات من Claude ومن Codex وغير ملتزم بها في Git.

## 2. مصادر السياق والمرجع

1. محادثة Claude الكاملة:

   C:\Users\hamee\.claude\projects\c--Users-hamee-Desktop-venom-router\7463f8fd-e5d4-4cae-848f-efde400ee6b6.jsonl

2. المرجع التقني المستخرج من تطبيق ClinePass القديم العامل:

   C:\Users\hamee\Desktop\venom-router\docs\evidence\clinepass-legacy-wire-reference.md

3. خطة دورة Live Models التي كتبها Codex:

   C:\Users\hamee\Desktop\venom-router\docs\superpowers\plans\2026-08-04-live-model-lifecycle.md

4. صور الحالة الحية التي أرسلها المالك:

   - C:\Users\hamee\AppData\Local\Temp\codex-clipboard-a0ece4fc-3b27-4a1b-a5e9-5c30cc176428.png
   - C:\Users\hamee\AppData\Local\Temp\codex-clipboard-c5faa249-9f95-44ca-ac2d-9f5b5b9979cc.png
   - C:\Users\hamee\AppData\Local\Temp\codex-clipboard-1e4eea45-38ac-41a0-9df9-fbfbb73fefd7.png

كلمة مرور لوحة التحكم قُدمت في المحادثة الأصلية، لكنها غير محفوظة هنا عمدًا حتى لا نضع سرًا نصيًا داخل المستودع. استخرجها من سجل الجلسة أو اطلبها مرة واحدة فقط من المالك.

## 3. قواعد المالك التي يجب اعتبارها عقد عمل

- العمل على main فقط. لا تنشئ فرعًا ولا تعمل commit أو push إلا بطلب صريح.
- افحص التعديلات السابقة كلها. لا تحافظ على تعديل لمجرد أنه موجود؛ أصلح التالف واحذف غير المفيد.
- لا تحذف بيانات أو ملفات خارج النطاق. أي حذف داخل نطاق المهمة يجب أن يكون محسوبًا ومثبتًا.
- Design_System ليس ممنوعًا من الإصلاح. المطلوب الالتزام به لمنع أنماط محلية عشوائية داخل الصفحات. إذا كان الخطأ فعليًا في النظام نفسه، أصلحه في المصدر المشترك واختبر المستهلكين.
- الفحص البصري يجب أن يكون على 8088، وليس سطح 8081 المضمّن المختلف.
- 8088 هو frontend تطوير live reload، لكنه يجب أن يشارك backend/auth/data الحقيقية مع 8081. لا تنشئ backend منفصلًا أو قاعدة dev منفصلة.
- لا تعتبر build أو unit tests وحدها إثباتًا. يجب التحقق من النظام الحي: Network، API، قاعدة البيانات/التوقيتات، والسلوك المرئي.
- اختبر الثيمين والنوافذ الواسعة والضيقة.
- لا تدّع أن التحديث لحظي لمجرد أن عبارة منذ X دقائق تتحرك؛ يجب إثبات طلبات تلقائية وبيانات persisted تتغير.
- عند التسليم، اذكر المشاكل المتبقية بوضوح ولا تخفِها.
- المطلوب مشروع Enterprise: بنية حالة موحدة، مصادر حقيقة typed، لا parsing لنصوص الأخطاء، لا شارات أو ألوان متضاربة.

## 4. عقد Cline/ClinePass الصحيح

### ClinePass OAuth الحالي

- اسم المزود: ClinePass.
- نوع المصادقة: OAuth فقط.
- مخصص فقط للحسابات ذات اشتراك ClinePass نشط.
- مدفوع فقط؛ لا يوجد خيار Free/Paid يدوي لهذا المزود.
- اكتشاف الاشتراك يتم آليًا من دليل المزود بعد تسجيل الدخول.
- لا يعرض شارة عامة باسم CLINEPASS ولا شارة Funding عامة Paid أو Unknown.
- الحساب السليم المشترك يمكن أن يعرض Pass active.
- الحساب غير المشترك يجب أن يعرض Subscription required برسالة واضحة.
- النماذج المستوردة لهذا المزود يجب أن تكون مجموعة clinePass فقط، لا recommended ولا free.

### Cline API Keys المستقبلي

- مزود منفصل اسمه Cline.
- نوع المصادقة: API Keys.
- يختار المالك يدويًا Free أو Paid لعدم وجود اكتشاف تلقائي.
- Free يجلب النماذج المجانية العاملة فقط.
- Paid يجلب كل النماذج المناسبة، لكن بدون نماذج ClinePass.
- هذا المزود المستقبلي خارج نطاق التنفيذ الحالي، لكن لا تخلط قواعده مع ClinePass OAuth.

## 5. الحالة الحية الحالية للحسابين

حسب آخر صورة على 8088:

- ipfox111@gmail.com:
  - health/display: degraded.
  - السبب: لا يوجد اشتراك ClinePass نشط.
  - غير تشغيلي.
  - لا يجب أن يملك نماذج حية أو رصيدًا/كوتا قديمة.
  - يجب أن يعاد فحص الاشتراك دوريًا؛ لا يصح اعتباره terminal للأبد لأن الاشتراك قد يضاف لاحقًا.

- support@venom-drm.com:
  - health/display: expired.
  - الرسالة الحالية: OAuth session expired or was revoked — sign in again.
  - غير تشغيلي حاليًا.
  - يفترض المالك أن الحساب أصلاً مشترك ويجب أن يعمل.
  - لم يُثبت بعد أن token refresh يصلحه تلقائيًا.
  - لم يُثبت إن كان refresh token الحالي ميتًا فعلًا أم أن التصنيف السابق للـ 401/403 كان خاطئًا.
  - قد يحتاج reauthentication مرة واحدة لاستعادة بيانات صحيحة، لكن لا يجوز الادعاء أن المشكلة حُلت قبل اختبار دورة كاملة بعد الدخول.

الهيدر الحالي يعرض تقريبًا:

- 0 working / 0 live
- 0 / 2 accounts healthy

هذا أكثر صدقًا من الرقم القديم 21، لكنه لا يكفي لإغلاق المهمة.

## 6. المطلوب من Live Models

- إعادة تسمية Models في السايدبار والعنوان إلى Live Models.
- الاحتفاظ بمسار URL /models للتوافق.
- الصفحة ليست أرشيفًا تاريخيًا.
- تعرض فقط نماذج مرتبطة بحساب:
  - connection_state = connected
  - health_state = healthy
  - reauth_in_progress = false
  - offering availability = available
- عند موت/توقف/انتهاء/عدم أهلية الحساب:
  - حذف account_model_offerings الخاصة به.
  - حذف operations/certifications/probe runs التابعة بالـ cascade.
  - حذف provider_model_aliases غير المستخدمة.
  - حذف models canonical اليتيمة.
- عند تعافي الحساب:
  - تشغيل discovery تلقائيًا وإعادة بناء كتالوجه من الحقيقة الحالية للمزود.
- كل العدادات في Providers وLive Models يجب أن تستخدم نفس تعريف live، لا تعريفات متباينة.

## 7. ما نفذه Codex في دورة Live Models

### تخزين وتنظيف

أُضيف:

- internal/storage/model_lifecycle.go
- internal/storage/model_lifecycle_test.go

الدالة ModelLifecycleRepo.PurgeInactive تنفذ transaction يحذف:

- العروض غير available.
- عروض أي حساب غير connected + healthy أو داخل reauth.
- aliases غير المستخدمة.
- models اليتيمة.

اختبارات مركزة نجحت قبل التوقف:

- TestModelLifecycleRepo_PurgeInactiveKeepsOnlyLiveCatalog
- TestModelLifecycleRepo_PurgeInactiveIsIdempotent

### قراءة API حية فقط

تعديل:

- internal/storage/catalog.go
- internal/httpapi/models.go
- internal/httpapi/models_test.go

أُضيف CatalogListParams.LiveOnly، ويُستخدم في /models و/offerings. الاختبار TestModelsHandler_ServesOnlyHealthyConnectedOfferings نجح في التشغيل المركز.

### الصيانة التلقائية

أُضيف:

- internal/httpapi/account_maintenance_tick.go
- internal/httpapi/account_maintenance_tick_test.go

السلوك الحالي المقصود:

- scheduler الأساسي كل 30 ثانية.
- health كل 5 دقائق.
- quota كل 15 دقيقة.
- discovery للحساب السليم كل 15 دقيقة.
- purge بعد كل sweep.

الاختبارات المركزة التي نجحت:

- TestAccountMaintenanceTick_PurgesInactiveModelsAfterEverySweep
- TestAccountMaintenanceTick_DiscoversRecoveredAndPeriodicallyHealthyAccounts
- وباقي مجموعة AccountMaintenanceTick المركزة وقتها.

هذا الجزء يحتاج مراجعة هندسية جديدة قبل اعتماده، خاصة ترتيب refresh/health/discovery/quota عند تغير الحالة داخل نفس الدورة.

### واجهة Live Models والعدادات

عُدلت:

- dashboard/src/shell/nav.ts
- dashboard/src/models/ModelsSurface.tsx
- dashboard/src/models/ModelsSurface.test.tsx
- dashboard/src/shell/AppShell.test.tsx
- dashboard/src/test/journey.flow.test.tsx
- dashboard/src/fleet/FleetOverview.tsx
- dashboard/src/fleet/ProviderRow.tsx
- dashboard/src/fleet/AccountRow.tsx

تم:

- تسمية Live Models.
- تغيير empty/loading/error copy إلى live.
- تصفية offerings في FleetOverview على الحسابات السليمة قبل حساب الإجمالي.
- النص الحالي للعداد: N working / N live.
- model count للحساب غير السليم = 0.

## 8. ما كان موجودًا من محاولة Claude وما زال في الـ worktree

يوجد عدد كبير من تغييرات OAuth/ClinePass/token refresh غير ملتزم بها. أهم الملفات:

- internal/accounts/application/token_refresh_service.go
- internal/accounts/application/token_refresh_service_test.go
- internal/httpapi/token_refresh_tick.go
- internal/httpapi/token_refresh_tick_test.go
- internal/providers/clinepass.go
- internal/providers/clinepass_test.go
- internal/providers/oauthexpiry.go
- internal/providers/oauthexpiry_test.go
- internal/httpapi/clinepass_usability.go
- internal/httpapi/clinepass_usability_test.go
- internal/httpapi/provider_registry.go
- internal/accounts/application/credential_service.go
- internal/storage/account_credentials.go
- internal/execution/nativeoauth.go
- internal/httpapi/oauth.go
- dashboard/src/fleet/usePollingRefresh.ts
- dashboard/src/fleet/usePollingRefresh.test.ts
- dashboard/src/fleet/quotaWindows.ts
- dashboard/src/fleet/ConnectDialog.tsx

Codex لم يثبت end-to-end أن هذه المنظومة تحافظ على جلسة support@venom-drm.com تلقائيًا. لا تفترض صحة هذه الملفات بسبب وجود tests فقط. راجعها كمنظومة واحدة.

السلوك المكتوب حاليًا:

- frontend polling كل 10 ثوانٍ عندما الصفحة visible.
- token refresh lead = 15 دقيقة قبل انتهاء access token.
- refresh failure cooldown = 15 دقيقة.
- provider 401/403 وحده لا يفترض أن يقتل refresh token؛ التصنيف النهائي يفترض وجود marker صريح مثل invalid_grant.

لكن القبول الحقيقي لم يتم: لا يوجد إثبات حي لدورة login → refresh rotation → persisted credential → health/quota/model recovery → استمرار العمل بعد وقت كافٍ.

## 9. أخطاء Codex التي يجب على الوكيل التالي عدم البناء فوقها

### 9.1 اشتقاق نوع المشكلة من نص الرسالة

في dashboard/src/fleet/AccountRow.tsx يُحسب subscriptionRequired حاليًا عن طريق البحث داخل last_health_error عن:

no active clinepass subscription

هذا خطأ معماري. الواجهة لا يجب أن تفهم نوع الحالة من نص بشري. المطلوب field typed من backend مثل:

- operational_status: operational | non_operational | transitional | disabled
- attention_code: subscription_required | reauth_required | provider_unavailable | credential_invalid | null
- recovery_action: auto_recheck | reauthenticate | none

كل الاستهلاك: لون المزود، بنية الصف، quota visibility، model eligibility، action slot، يجب أن يعتمد على نفس الحقول typed.

### 9.2 اختلاف تصميم حسابين كلاهما غير تشغيلي

الحالة المرئية الحالية فوضوية:

- صف ipfox له rail أصفر وindex أصفر.
- صف support له rail أحمر وindex محايد.
- الأول له Subscription required بعد البريد، والثاني يترك فراغًا في نفس المكان.
- الأول له Account access unavailable، والثاني Sign-in required.
- محاذاة المساحات والإجراءات مختلفة.
- الصفان كلاهما non-operational لكن لا يشتركان في state template موحد.

هذا هو السبب المباشر لتوقف المالك عن العمل مع Codex.

### 9.3 خلط severity مع reason

الخطأ الحالي يربط لون إطار الصف بسبب المشكلة التفصيلي. الصحيح:

- بنية الصف وrail/index يعبّران عن operational state العام.
- reason badge والنص والإجراء يعبّرون عن السبب.
- الحسابان غير التشغيليين يجب أن يشتركا في نفس البنية الأساسية.

### 9.4 الحل البصري الصحيح المطلوب

لا تبدأ بتغيير CSS عشوائي. صمم state contract أولًا ثم snapshot/component tests.

اقتراح قبول واضح ومتوافق مع ملاحظات المالك:

- جميع الصفوف تستخدم نفس container والـ index المحايد.
- لا يوجد full-row red/yellow fill.
- لا يوجد rail أصفر لحساب وrail أحمر لآخر وهما في نفس operational class.
- non-operational يستخدم treatment واحد هادئ في الحسابين؛ يمكن أن يكون rail واحد muted أو بدون rail إذا كان alert كافيًا.
- يوجد status slot ثابت بعد البريد في كل صف غير تشغيلي.
- استخدم نفس tone للحالة العامة في الحسابين، مثل Action required، ثم ضع السبب المحدد داخل نص موحد:
  - Subscription required
  - Sign in again
- alert له نفس title hierarchy، padding، width policy، وموضع ثابت.
- actions تبقى في عمود ثابت؛ اختلاف زر reauth مسموح لكن لا يغيّر هندسة الصف.
- meta line له نفس الصيغة:
  - Updates paused · Subscription will be checked automatically
  - Updates paused · Sign in again to resume
- model button في الحسابين = 0 ومعطل.
- quota/balance غير ظاهرة في الحسابين.
- provider header:
  - أخضر فقط عندما كل المطلوب سليم وفق policy.
  - برتقالي عندما يوجد حساب سليم وآخر يحتاج مراجعة.
  - أحمر/critical فقط عندما لا يوجد أي حساب تشغيلي، كما في الحالة الحالية 0/2.

لا تعتمد هذه الصياغة حرفيًا إن كان Design_System يملك state component أفضل؛ المهم هو وحدة البنية والمصدر.

### 9.5 استخدام TypedErrorDisplay الخاطئ

قبل آخر تعديل كان AccountRow يستخدم TypedErrorDisplay مع retryable=false، فظهر not retryable على الحسابين. Codex استبدله بـ Alert، وهذا أزال العبارة، لكنه لم يحل state model.

لا تعِد not retryable لحالة حساب. هذا component خاص error envelope لطلب API، وليس account lifecycle state.

### 9.6 الرصيد والكوتا

Codex أضاف liveQuotaWindows محليًا في AccountRow:

- لا يعرض quota/balance إذا display_status غير healthy.
- لا يعرض provider evidence إلا available + fresh.

هذا يخفي القيم القديمة بصريًا، لكنه ما زال policy مشتقًا في الواجهة. الأفضل أن API يقدم current evidence واضحًا، أو أن selector مشترك واحد يستخدمه كل المستهلكين. لا تكرر predicate في عدة صفحات.

### 9.7 التوقيتات

النص الحالي تحوّل إلى صيغ مثل:

- Subscription checked ... · Usage unavailable
- Live updates paused · Sign in again
- Usage updated ... · Health checked ...

الفكرة أفضل من Quota/Checked الغامضة، لكن القبول لم يتم بصريًا ولا حيًا. يجب أن تكون observed timestamps مستقلة:

- health_checked_at
- quota_observed_at
- token_refreshed_at أو credential_expires_at عند الحاجة
- models_discovered_at

ولا تعتبر relative time المتحرك دليل fetch.

## 10. تنظيفات قام بها Codex

- أزال instrumentation خطيرًا من internal/httpapi/oauth.go كان يكتب OAuth debug data وcodePrefix إلى:

  C:\Users\hamee\Desktop\venom-router\debug-db56d2.log

- حذف ملف debug-db56d2.log بعد التحقق من مساره. الحذف مقصود لأنه قد يحتوي أجزاء حساسة.
- أعاد 70 ملف Dashboard كانت تغييراتها formatting-only إلى حالتها الأصلية بعد مقارنة read-only.
- حذف مستندين قديمين كانا يثبتان قرارات صارت عكس المطلوب: full critical row والاحتفاظ التاريخي بالنماذج.
- أبقى مرجع wire الحقيقي وخطة Live Models الحالية.

## 11. حالة Git الحالية وقت كتابة التقرير

- الفرع: main.
- لا يوجد commit أو push من Codex.
- إجمالي status: 65 مسارًا.
- tracked modified: 43.
- untracked: 22.
- diff tracked وقت الفحص: 43 files changed، 2971 insertions، 764 deletions.

لا تستخدم git reset --hard ولا git checkout عشوائي. اعمل inventory، صنّف كل ملف إلى:

1. Live Models lifecycle.
2. ClinePass OAuth/token refresh.
3. UI state model.
4. اختبار/توثيق ضروري.
5. ضوضاء أو تنفيذ متناقض يجب حذفه.

ثم نظف بالتدرج مع اختبارات.

## 12. حالة الاختبارات الفعلية

### نجح

- go test مركز لـ ModelLifecycleRepo.
- go test مركز لـ CatalogRepo/Models handlers.
- go test مركز لـ AccountMaintenanceTick.
- dashboard FleetOverview: 41/41 نجحت بعد آخر تعديل.
- dashboard AppShell + ModelsSurface نجحت في التشغيل المركز السابق.
- تشغيل dashboard كامل قبل آخر تعديل في journey:
  - 495 passed.
  - 2 failed فقط لأن tests كانت تبحث عن Models بدل Live Models.
- تم تعديل journey بعد ذلك إلى Live Models، لكن التشغيل الذي كان يتحقق منها ومعه go test ./... أوقفه Codex فور طلب المالك التوقف.

### لم يكتمل أو لم يثبت

- go test ./... بعد الحالة النهائية: لم يكتمل بسبب الإيقاف.
- dashboard full test بعد تعديل journey: لم يكتمل.
- npm typecheck: لم يُشغّل بعد الحالة النهائية.
- npm lint: لم يُشغّل بعد الحالة النهائية.
- npm build: لم يُشغّل بعد الحالة النهائية.
- git diff --check كان نظيفًا قبل آخر مجموعة صغيرة من التعديلات، ويجب إعادته.
- visual QA كامل في light/dark وعروض مختلفة: فشل عمليًا؛ صور المالك كشفت عدم الاتساق.
- live API/network/persistence proof: غير مكتمل.
- token refresh end-to-end: غير مثبت.

## 13. ترتيب العمل المقترح للوكيل التالي

### المرحلة A — تثبيت الحالة قبل لمس الكود

1. اقرأ ملف Claude JSONL كاملًا.
2. اقرأ مرجع clinepass-legacy-wire-reference.md كاملًا.
3. خذ git status وdiff inventory.
4. افتح 8088 وصوّر الحالة الحالية في light/dark.
5. افحص Network endpoints للحسابات والعروض والموديلات.
6. افحص DB الحقيقية المشتركة، لا dev DB.

### المرحلة B — تصميم مصدر حقيقة typed

1. حدد owner واحد لحالة الحساب في backend.
2. أضف enum/code مستقر للسبب؛ امنع UI substring parsing.
3. اكتب contract tests لـ:
   - healthy subscribed
   - subscription required
   - reauth required
   - transient provider failure
   - stopped/disconnected
4. اجعل provider aggregate، live model eligibility، quota visibility، row treatment كلها تستهلك نفس المصدر.

### المرحلة C — توحيد AccountRow

1. اكتب tests تفشل على الشكل الحالي:
   - نفس container class للحسابين non-operational.
   - نفس index tone.
   - نفس status slot.
   - لا stale quota/balance.
   - لا not retryable.
   - model count = 0.
2. أنشئ AccountStatePresentation selector مركزيًا:
   - operationalClass
   - badgeLabel/tone
   - alertTitle/message
   - metaText
   - primaryRecoveryAction
3. اجعل JSX واحدًا بلا فروع تغير layout.
4. استخدم Design_System، وعدله في المصدر فقط إذا احتجت primitive ناقصًا فعليًا.
5. راجع CSS واحذف modifiers المتعارضة:
   - vnd-account--subscription-required
   - vnd-account--expired
   أو استبدلهما modifier واحدًا للحالة العامة.

### المرحلة D — إثبات OAuth refresh

1. لا تسجّل أي token أو code.
2. صنف 401/403 من refresh بناءً على provider body marker، لا status فقط.
3. اختبر credential rotation persistence.
4. اختبر أن refresh الناجح يعيد account إلى probeable state.
5. بعد reauth الحقيقي لحساب support، راقب:
   - credential expiry/rotation
   - health timestamp
   - quota observed timestamp
   - model discovery timestamp
   - بقاء الحساب عاملًا بعد تجاوز access-token expiry المعتاد.
6. إن احتاج الحساب login جديدًا مرة واحدة، قل ذلك للمالك بوضوح، لكن أثبت أنه لن يحتاجه كل ساعة.

### المرحلة E — إغلاق Live Models

1. شغّل purge على DB الاختبارية وتحقق من graph deletion.
2. تحقق أن API لا يعرض dead offerings حتى قبل purge.
3. تحقق أن DB الحية تنظفت من عروض الحسابين غير التشغيليين.
4. تحقق أن /models فارغة عندما 0 accounts healthy.
5. بعد تعافي حساب، تحقق أن discovery يعيد models تلقائيًا.
6. تأكد أن Providers وLive Models يعرضان نفس الأعداد.

### المرحلة F — قبول نهائي

شغّل على الأقل:

- go test ./... -count=1
- npm.cmd test -- --run
- npm.cmd run typecheck
- npm.cmd run lint
- npm.cmd run build
- git diff --check

ثم تحقق حيًا على 8088 في:

- light + dark
- desktop + narrow
- provider collapsed + expanded
- الحسابان غير التشغيليين
- حساب متعافٍ إن أمكن
- Live Models empty ثم repopulated
- Network polling بدون reload يدوي

## 14. الملفات ذات الأولوية للمراجعة

Backend:

- internal/providers/clinepass.go
- internal/accounts/application/token_refresh_service.go
- internal/httpapi/token_refresh_tick.go
- internal/httpapi/account_maintenance_tick.go
- internal/httpapi/clinepass_usability.go
- internal/httpapi/oauth.go
- internal/httpapi/models.go
- internal/storage/catalog.go
- internal/storage/model_lifecycle.go
- internal/storage/accounts.go
- internal/storage/account_credentials.go

Frontend:

- dashboard/src/fleet/AccountRow.tsx
- dashboard/src/fleet/FleetOverview.tsx
- dashboard/src/fleet/ProviderRow.tsx
- dashboard/src/fleet/fleet.css
- dashboard/src/fleet/QuotaSummary.tsx
- dashboard/src/fleet/quotaWindows.ts
- dashboard/src/fleet/usePollingRefresh.ts
- dashboard/src/models/ModelsSurface.tsx
- dashboard/src/shell/nav.ts

Tests:

- internal/providers/clinepass_test.go
- internal/accounts/application/token_refresh_service_test.go
- internal/httpapi/token_refresh_tick_test.go
- internal/httpapi/account_maintenance_tick_test.go
- internal/storage/model_lifecycle_test.go
- internal/storage/catalog_test.go
- internal/httpapi/models_test.go
- dashboard/src/fleet/FleetOverview.test.tsx
- dashboard/src/models/ModelsSurface.test.tsx
- dashboard/src/test/journey.flow.test.tsx

## 15. تعريف الانتهاء الحقيقي

لا تعتبر المهمة منتهية إلا إذا تحققت كل النقاط التالية:

- صفا الحسابين غير التشغيليين لهما بنية وتصميم موحدان بلا rail/index متضارب.
- الاختلاف الوحيد الظاهر هو السبب وإجراء التعافي المناسب، لا هندسة مختلفة.
- لا توجد CLINEPASS/Paid/Unknown الخاطئة.
- لا توجد not retryable.
- لا quota/balance قديمة للحساب غير التشغيلي.
- provider header يعكس 0/2 أو mixed truth بدقة.
- لا يظهر أي model عندما لا يوجد حساب سليم.
- dead provider models محذوفة فعليًا من DB، لا مخفية فقط.
- recovery يعيد discovery تلقائيًا.
- polling مثبت بطلبات Network وبيانات persisted متغيرة.
- token refresh مثبت end-to-end أو المشكلة المتبقية موصوفة بصدق.
- كل الاختبارات والبناء والفحص البصري ناجحة.

## 16. رسالة صريحة للوكيل التالي

لا تبنِ فوق الشكل الحالي ولا تحاول تجميل الاختلافات بلون أو margin جديد. المشكلة الأساسية هي غياب state contract موحد واستخدام UI parsing لنص الخطأ. ابدأ من مصدر الحقيقة في backend، ثبّت contract باختبارات، ثم اجعل الصف template واحدًا. حافظ على ما يثبت أنه صحيح من Live Models وtoken refresh، واحذف أي جزء متناقض أو غير مثبت. المالك لا يريد مزيدًا من الادعاءات؛ يريد نظامًا حيًا متسقًا ومثبتًا على 8088.
