package secret

// Provider token rules.
//
// The patterns below are ported from the provider rules of Gitleaks
// (github.com/gitleaks/gitleaks, config/gitleaks.toml and cmd/generate/config/
// rules/*.go), v8.x series, under its MIT licence; the notice is in NOTICE
// beside this file and must travel with any copy of these patterns.
//
// The rule set was diffed against upstream config/gitleaks.toml at commit
// 5f6ec56 (2026-10-23). The patterns are an adaptation and not a byte-for-byte
// copy: Go's regexp is RE2, which has no lookaround, so a rule that leaned on
// one is rewritten to need none, and upstream's trailing
// `(?:[\x60'"\s;]|\\[nr]|$)` terminator becomes \b or a capture group. A rule
// whose only anchor was a lookaround is left out. Only rules with a
// distinctive prefix or a named-provider context are here. Gitleaks'
// generic-api-key is not: that job is the entropy check in secret.go, which
// runs on values beside `key`, `token`, `secret` and `password` and is what
// catches a token with no known shape.
//
// Nothing here reads a file or the network at runtime.
//
// Intentionally not ported, and why:
//   - the keyword-context rules for services with no token prefix (algolia,
//     datadog, okta, cloudflare, twitter-api-key and the like): their value is
//     a bare 32-40 character hex or alphanumeric string, the shape of a git
//     hash without its keyword. The entropy check masks a mixed-alphabet value
//     beside a key/token/secret name under MaskAll; a pure-hex value it
//     deliberately does not (see corpus_test.go).
//   - kubernetes-secret-yaml, curl-auth-header, curl-auth-user: they span
//     lines, and masking works a line at a time. An Authorization header inside
//     a curl command is already masked by secret.go; curl -u user:password is
//     not.
//   - hashicorp-tf-password, pkcs12-file: upstream gates them on a file path,
//     which a transcript line does not have, and a bare "password" context
//     would mask ordinary configuration prose.
//   - jwt-base64 and sidekiq-secret: no distinctive prefix of their own.
//   - sourcegraph-access-token's bare 40-hex alternative: a git hash.
//   - private-key and slack-webhook-url live in secret.go (privateKeyBlock and
//     lineShapes); so does the Sidekiq Enterprise URL, because it must run
//     before the URL-password pass.
//
// Also considered and left out, because they masked ordinary text in the
// corpus (testdata/ordinary.txt) or have no shape distinctive enough:
//   - hashicorp-vault-legacy `s.` + 24 alphanumerics: matches any short
//     dotted identifier ending in a 24-letter word.
//   - mailgun-signing-key `[a-h0-9]{32}-[a-h0-9]{8}-[a-h0-9]{8}`: has the
//     length and alphabet of a hex hash joined by hyphens, as git log output
//     and content-addressed paths are.
//   - discord-bot-token: its only literal is the "." between its parts, which
//     is in nine lines of ten, so it would run a regexp over all of them.
//     secret.go's tokenShapes already masks it, word by word.
//   - airtable's bare `pat` prefix is the start of "path" and "patch": the
//     rule is kept only behind the word airtable.
//   - generic-api-key, and the keyword-context rules for services with no
//     prefix (Datadog, Okta, Zendesk, ...): a bare 32-40 character hex or
//     alphanumeric value is indistinguishable from a git hash without its
//     keyword, and the entropy check already owns that case.
//
// Each rule carries keywords, the way Gitleaks does: a lowercase literal every
// match contains. A line is run through a rule's regexp only if it holds one,
// because Go's regexp cannot skip ahead on patterns that open with \b or a
// class, and a transcript is megabytes of lines that mention no provider.

import (
	"regexp"
	"strings"
	"sync"
)

// ctx is the lead-in of a rule with no distinctive prefix of its own: the
// provider's name near an assignment, as Gitleaks writes it. The value that
// follows is what is masked.
func ctx(names string) string {
	return `(?i)\b(?:` + names + `)[\w ./-]{0,25}["']?\s*[:=]\s*["']?`
}

type tokenRule struct {
	id       string
	keywords []string // lowercase; at least one is in every match
	pattern  string
	group    int // capture holding the secret; 0 masks the whole match
}

var tokenRules = []tokenRule{
	// AWS
	{"aws-access-token", []string{"akia", "asia", "abia", "acca", "a3t"}, `\b((?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16})\b`, 1},
	// GitHub
	{"github-pat", []string{"ghp_"}, `\bghp_[0-9a-zA-Z]{36}\b`, 0},
	{"github-fine-grained-pat", []string{"github_pat_"}, `\bgithub_pat_[0-9a-zA-Z_]{82}\b`, 0},
	{"github-oauth", []string{"gho_"}, `\bgho_[0-9a-zA-Z]{36}\b`, 0},
	{"github-app-token", []string{"ghu_", "ghs_"}, `\b(?:ghu|ghs)_[0-9a-zA-Z]{36}\b`, 0},
	{"github-refresh-token", []string{"ghr_"}, `\bghr_[0-9a-zA-Z]{36}\b`, 0},
	// GitLab
	// The routable forms carry a dotted routing suffix, so the fixed-length
	// rules below stop short of it (their \b cannot follow a dot-less 20).
	{"gitlab-pat-routable", []string{"glpat-"}, `\bglpat-[0-9a-zA-Z_-]{27,}\.[0-9a-z]{9}\b`, 0},
	{"gitlab-runner-authentication-token-routable", []string{"glrt-"}, `\bglrt-t\d_[0-9a-zA-Z_-]{27,}\.[0-9a-z]{9}\b`, 0},
	{"gitlab-feature-flag-client-token", []string{"glffct-"}, `\bglffct-[0-9a-zA-Z_-]{20}\b`, 0},
	{"gitlab-session-cookie", []string{"_gitlab_session="}, `_gitlab_session=([0-9a-z]{32})\b`, 1},
	{"gitlab-pat", []string{"glpat-"}, `\bglpat-[0-9a-zA-Z_-]{20}\b`, 0},
	{"gitlab-pipeline-trigger-token", []string{"glptt-"}, `\bglptt-[0-9a-f]{40}\b`, 0},
	{"gitlab-runner-registration-token", []string{"gr1348941"}, `\bGR1348941[0-9a-zA-Z_-]{20}\b`, 0},
	{"gitlab-runner-auth-token", []string{"glrt-"}, `\bglrt-[0-9a-zA-Z_-]{20,}\b`, 0},
	{"gitlab-cicd-job-token", []string{"glcbt-"}, `\bglcbt-[0-9a-zA-Z]{1,5}_[0-9a-zA-Z_-]{20}\b`, 0},
	{"gitlab-deploy-token", []string{"gldt-"}, `\bgldt-[0-9a-zA-Z_-]{20}\b`, 0},
	{"gitlab-feed-token", []string{"glft-"}, `\bglft-[0-9a-zA-Z_-]{20}\b`, 0},
	{"gitlab-oauth-app-secret", []string{"gloas-"}, `\bgloas-[0-9a-zA-Z_-]{64}\b`, 0},
	{"gitlab-scim-token", []string{"glsoat-"}, `\bglsoat-[0-9a-zA-Z_-]{20}\b`, 0},
	{"gitlab-incoming-mail-token", []string{"glimt-"}, `\bglimt-[0-9a-zA-Z_-]{25}\b`, 0},
	{"gitlab-agent-token", []string{"glagent-"}, `\bglagent-[0-9a-zA-Z_-]{50}\b`, 0},
	// Slack (the webhook URL is a lineShape in secret.go)
	{"slack-bot-token", []string{"xoxb-"}, `\bxoxb-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9-]*`, 0},
	// The older, shorter bot token shape, still issued by legacy apps.
	{"slack-legacy-bot-token", []string{"xoxb-"}, `\bxoxb-[0-9]{8,14}-[a-zA-Z0-9]{18,26}`, 0},
	{"slack-user-token", []string{"xoxp-", "xoxe-"}, `\bxox[pe](?:-[0-9]{10,13}){3}-[a-zA-Z0-9-]{28,34}`, 0},
	{"slack-app-token", []string{"xapp-"}, `(?i)\bxapp-\d-[a-z0-9]+-\d+-[a-z0-9]+`, 0},
	{"slack-config-access-token", []string{"xoxe.xox"}, `(?i)\bxoxe\.xox[bp]-\d-[a-z0-9]{163,166}`, 0},
	{"slack-config-refresh-token", []string{"xoxe-"}, `(?i)\bxoxe-\d-[a-z0-9]{146}`, 0},
	{"slack-legacy-token", []string{"xoxo-", "xoxs-"}, `\bxox[os]-\d+-\d+-\d+-[a-fA-F\d]+`, 0},
	{"slack-legacy-workspace-token", []string{"xoxa-", "xoxr-"}, `\bxox[ar]-(?:\d-)?[0-9a-zA-Z]{8,48}`, 0},
	// Stripe, Square, Twilio, SendGrid, Mailgun, Mailchimp, Braintree, PayPal-adjacent
	{"stripe-access-token", []string{"sk_test", "sk_live", "sk_prod", "rk_test", "rk_live", "rk_prod"}, `\b[sr]k_(?:test|live|prod)_[a-zA-Z0-9]{10,99}\b`, 0},
	{"square-access-token", []string{"sq0atp-", "eaaa"}, `\b(?:sq0atp-[0-9A-Za-z_-]{22}|EAAA[a-zA-Z0-9_+=-]{60})\b`, 0},
	{"square-oauth-secret", []string{"sq0csp-"}, `\bsq0csp-[0-9A-Za-z_-]{43}\b`, 0},
	{"twilio-api-key", hexKeywords("sk", ""), `\bSK[0-9a-fA-F]{32}\b`, 0},
	{"sendgrid-api-token", []string{"sg."}, `\bSG\.[a-zA-Z0-9=_\-.]{66}\b`, 0},
	{"mailgun-private-api-token", hexKeywords("key-", ""), `\bkey-[a-f0-9]{32}\b`, 0},
	{"mailgun-pub-key", []string{"pubkey-"}, `\bpubkey-[a-f0-9]{32}\b`, 0},
	{"mailchimp-api-key", digitKeywords("-us"), `\b[a-f0-9]{32}-us[0-9]{1,2}\b`, 0},
	{"braintree-access-token", []string{"access_token$"}, `\baccess_token\$production\$[0-9a-z]{16}\$[0-9a-f]{32}\b`, 0},
	{"sendinblue-api-token", []string{"xkeysib-"}, `\bxkeysib-[a-f0-9]{64}-[a-zA-Z0-9]{16}\b`, 0},
	// Package registries
	{"npm-access-token", []string{"npm_"}, `\bnpm_[a-zA-Z0-9]{36}\b`, 0},
	{"pypi-upload-token", []string{"pypi-agei"}, `\bpypi-AgEIcHlwaS5vcmc[\w-]{50,1000}`, 0},
	{"rubygems-api-token", []string{"rubygems_"}, `\brubygems_[a-f0-9]{48}\b`, 0},
	{"clojars-api-token", []string{"clojars-"}, `\bCLOJARS-[a-zA-Z0-9]{60}\b`, 0},
	{"artifactory-api-key", []string{"akcp"}, `\bAKCp[A-Za-z0-9]{69}\b`, 0},
	{"nuget-api-key", []string{"oy2"}, `\boy2[a-z0-9]{43}\b`, 0},
	// AI providers
	{"openai-api-key", []string{"sk-proj-", "sk-svcacct-", "sk-admin-", "t3blbkfj"}, `\b(?:sk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{40,}|sk-[A-Za-z0-9]{20}T3BlbkFJ[A-Za-z0-9]{20})`, 0},
	{"anthropic-api-key", []string{"sk-ant-"}, `\bsk-ant-(?:api03|admin01)-[A-Za-z0-9_-]{80,}`, 0},
	{"huggingface-access-token", []string{"hf_"}, `\bhf_[a-zA-Z]{34}\b`, 0},
	{"huggingface-org-token", []string{"api_org_"}, `\bapi_org_[a-zA-Z]{34}\b`, 0},
	{"groq-api-key", []string{"gsk_"}, `\bgsk_[A-Za-z0-9]{52}\b`, 0},
	{"xai-api-key", []string{"xai-"}, `\bxai-[A-Za-z0-9]{80}\b`, 0},
	{"perplexity-api-key", []string{"pplx-"}, `\bpplx-[A-Za-z0-9]{48}\b`, 0},
	{"replicate-api-token", []string{"r8_"}, `\br8_[A-Za-z0-9]{37}\b`, 0},
	{"pinecone-api-key", []string{"pcsk_"}, `\bpcsk_[A-Za-z0-9_]{50,}`, 0},
	// Google Cloud
	{"gcp-api-key", []string{"aiza"}, `\bAIza[\w-]{35}\b`, 0},
	{"gcp-oauth-access-token", []string{"ya29."}, `\bya29\.[0-9A-Za-z_-]{20,}`, 0},
	{"gcp-service-account-key-id", []string{"private_key_id"}, `"private_key_id"\s*:\s*"([0-9a-f]{40})"`, 1},
	{"gcp-oauth-client-secret", []string{"gocspx-"}, `\bGOCSPX-[a-zA-Z0-9_-]{28}\b`, 0},
	// Azure
	{"azure-ad-client-secret", []string{"q~"}, `(?:^|[\\'"\x60\s>=:(,)])([a-zA-Z0-9_~.]{3}\dQ~[a-zA-Z0-9_~.-]{31,34})(?:$|[\\'"\x60\s<),])`, 1},
	{"azure-storage-account-key", []string{"accountkey="}, `(?i)\bAccountKey=([A-Za-z0-9+/]{86}==)`, 1},
	{"azure-service-bus-key", []string{"sharedaccesskey="}, `(?i)\bSharedAccessKey=([A-Za-z0-9+/]{43}=)`, 1},
	{"azure-devops-pat", []string{"azure", "devops", "ado_", "ado-"}, ctx("azure[\\w-]*|devops[\\w-]*|ado[_-][\\w-]*") + `([a-z2-7]{52})\b`, 1},
	// Cloud and platform hosts
	{"digitalocean-pat", []string{"dop_v1_"}, `\bdop_v1_[a-f0-9]{64}\b`, 0},
	{"digitalocean-oauth-token", []string{"doo_v1_"}, `\bdoo_v1_[a-f0-9]{64}\b`, 0},
	{"digitalocean-refresh-token", []string{"dor_v1_"}, `(?i)\bdor_v1_[a-f0-9]{64}\b`, 0},
	{"heroku-api-key-v2", []string{"hrku-"}, `\bHRKU-AA[0-9a-zA-Z_-]{58}\b`, 0},
	{"heroku-api-key", []string{"heroku"}, ctx("heroku") + `([0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12})\b`, 1},
	{"cloudflare-origin-ca-key", []string{"v1.0-"}, `\bv1\.0-[a-f0-9]{24}-[a-f0-9]{146}\b`, 0},
	{"fly-access-token", []string{"fo1_", "fm2_", "fm1r_", "fm1a_"}, `\b(?:fo1_[\w-]{43}|(?:fm1[ar]_|fm2_)[A-Za-z0-9+/=_-]{100,})`, 0},
	{"netlify-access-token", []string{"nfp_"}, `\bnfp_[A-Za-z0-9]{36}\b`, 0},
	{"supabase-access-token", []string{"sbp_"}, `\bsbp_[a-f0-9]{40}\b`, 0},
	{"planetscale-api-token", []string{"pscale_tkn_"}, `\bpscale_tkn_[\w=.-]{32,64}`, 0},
	{"planetscale-password", []string{"pscale_pw_"}, `\bpscale_pw_[\w=.-]{32,64}`, 0},
	{"planetscale-oauth-token", []string{"pscale_oauth_"}, `\bpscale_oauth_[\w=.-]{32,64}`, 0},
	{"scalingo-api-token", []string{"tk-us-"}, `\btk-us-[\w-]{48}\b`, 0},
	{"alibaba-access-key-id", []string{"ltai"}, `\bLTAI[a-zA-Z0-9]{20}\b`, 0},
	{"yandex-iam-token", []string{"aqvn"}, `\bAQVN[A-Za-z0-9_-]{35,38}\b`, 0},
	{"openshift-user-token", []string{"sha256~"}, `\bsha256~[\w-]{43}\b`, 0},
	{"tailscale-key", []string{"tskey-"}, `\btskey-(?:api|auth|client)-[A-Za-z0-9]{6,}(?:CNTRL)?-[A-Za-z0-9]{20,}`, 0},
	// Secrets and infrastructure tooling
	{"doppler-api-token", []string{"dp."}, `\bdp\.(?:pt|st|sa|ct|scim|audit)\.[a-zA-Z0-9]{40,44}\b`, 0},
	{"hashicorp-tf-api-token", []string{".atlasv1."}, `\b[a-zA-Z0-9]{14}\.atlasv1\.[a-zA-Z0-9_=-]{60,70}`, 0},
	// No upper bound and no trailing \b: a bound that the token exceeds would
	// make \b fail and the whole token go unmasked, not just its tail.
	{"vault-batch-token", []string{"hvb."}, `\bhvb\.[\w-]{90,}`, 0},
	{"vault-service-token", []string{"hvs.", "hvr."}, `\bhv[sr]\.[\w-]{90,}`, 0},
	{"pulumi-api-token", []string{"pul-"}, `\bpul-[a-f0-9]{40}\b`, 0},
	{"infracost-api-token", []string{"ico-"}, `\bico-[a-zA-Z0-9]{32}\b`, 0},
	{"age-secret-key", []string{"age-secret-key-1"}, `\bAGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58}`, 0},
	{"octopus-deploy-api-key", []string{"api-"}, `\bAPI-[A-Z0-9]{26}\b`, 0},
	{"prefect-api-token", []string{"pnu_"}, `\bpnu_[a-zA-Z0-9]{36}\b`, 0},
	{"grafana-cloud-api-token", []string{"glc_"}, `\bglc_[A-Za-z0-9+/]{32,400}={0,3}`, 0},
	{"grafana-service-account-token", []string{"glsa_"}, `\bglsa_[A-Za-z0-9]{32}_[A-Fa-f0-9]{8}\b`, 0},
	{"sentry-user-token", []string{"sntryu_"}, `\bsntryu_[a-f0-9]{64}\b`, 0},
	{"sentry-org-token", []string{"sntrys_"}, `\bsntrys_eyJ[A-Za-z0-9+/=_-]{40,}`, 0},
	{"newrelic-user-api-key", []string{"nrak-"}, `\bNRAK-[A-Za-z0-9]{27}\b`, 0},
	{"newrelic-insert-key", []string{"nrii-"}, `(?i)\bNRII-[a-z0-9-]{32}`, 0},
	{"newrelic-browser-api-token", []string{"nrjs-"}, `(?i)\bNRJS-[a-f0-9]{19}\b`, 0},
	{"dynatrace-api-token", []string{"dt0c01."}, `\bdt0c01\.[a-zA-Z0-9]{24}\.[a-zA-Z0-9]{64}\b`, 0},
	{"sonar-api-token", []string{"sqp_", "squ_", "sqa_"}, `\bsq[pua]_[a-f0-9]{40}\b`, 0},
	// Collaboration and productivity
	{"linear-api-key", []string{"lin_api_"}, `\blin_api_[a-zA-Z0-9]{40}\b`, 0},
	{"linear-oauth-token", []string{"lin_oauth_"}, `\blin_oauth_[a-zA-Z0-9]{40,}\b`, 0},
	{"notion-integration-token", []string{"ntn_"}, `\bntn_[0-9]{11}[A-Za-z0-9]{32}[A-Za-z0-9]{3}\b`, 0},
	{"notion-legacy-token", []string{"secret_"}, `\bsecret_[A-Za-z0-9]{43}\b`, 0},
	{"atlassian-api-token", []string{"atatt3"}, `\bATATT3[A-Za-z0-9_=-]{186}`, 0},
	{"airtable-pat", []string{"airtable"}, ctx("airtable[\\w-]*") + `(pat[A-Za-z0-9]{14}\.[a-f0-9]{64})`, 1},
	{"postman-api-token", []string{"pmak-"}, `\bPMAK-[a-f0-9]{24}-[a-f0-9]{34}\b`, 0},
	{"figma-pat", []string{"figd_"}, `\bfigd_[A-Za-z0-9_-]{40,}`, 0},
	{"typeform-token", []string{"tfp_"}, `\btfp_[a-z0-9_.=-]{59}\b`, 0},
	{"frameio-api-token", []string{"fio-u-"}, `\bfio-u-[A-Za-z0-9_=-]{64}\b`, 0},
	{"readme-api-token", []string{"rdme_"}, `\brdme_[a-z0-9]{70}\b`, 0},
	{"dropbox-short-lived-token", []string{"sl."}, `\bsl\.[A-Za-z0-9_-]{130,}`, 0},
	{"adobe-client-secret", []string{"p8e-"}, `\bp8e-[a-zA-Z0-9]{32}\b`, 0},
	{"mapbox-secret-token", []string{"sk.eyj"}, `\bsk\.eyJ[a-zA-Z0-9]{20,}\.[a-zA-Z0-9_-]{20,}`, 0},
	{"shopify-access-token", []string{"shpat_", "shpca_", "shppa_", "shpss_"}, `\bshp(?:at|ca|pa|ss)_[a-fA-F0-9]{32}\b`, 0},
	{"shippo-api-token", []string{"shippo_"}, `\bshippo_(?:live|test)_[a-fA-F0-9]{40}\b`, 0},
	{"duffel-api-token", []string{"duffel_"}, `\bduffel_(?:test|live)_[a-zA-Z0-9_=-]{43}\b`, 0},
	{"easypost-api-token", []string{"ezak", "eztk"}, `\bEZ[AT]K[a-zA-Z0-9]{54}\b`, 0},
	{"flutterwave-secret-key", []string{"flwseck"}, `\bFLWSECK(?:_TEST)?-[a-hA-H0-9]{32}-X\b`, 0},
	{"plaid-access-token", []string{"access-sandbox-", "access-development-", "access-production-"}, `\baccess-(?:sandbox|development|production)-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`, 0},
	// Chat
	{"discord-webhook", []string{"discord"}, `https://(?:ptb\.|canary\.)?discord(?:app)?\.com/api/webhooks/\d{17,20}/[A-Za-z0-9_-]{60,}`, 0},
	// Bearer-shaped
	{"jwt", []string{"eyj"}, `\beyJ[a-zA-Z0-9]{16,}\.eyJ[a-zA-Z0-9/\\_-]{16,}\.(?:[a-zA-Z0-9/\\_-]{10,}={0,2})?`, 0},
	// Added after diffing against upstream (see the header): rules whose shape
	// is distinctive on its own, or whose provider name is part of the match.
	{"1password-secret-key", []string{"a3-"}, `\bA3-[A-Z0-9]{6}-(?:[A-Z0-9]{11}|[A-Z0-9]{6}-[A-Z0-9]{5})-[A-Z0-9]{5}-[A-Z0-9]{5}-[A-Z0-9]{5}\b`, 0},
	{"1password-service-account-token", []string{"ops_"}, `\bops_eyJ[a-zA-Z0-9+/]{250,}={0,3}`, 0},
	{"artifactory-reference-token", []string{"cmvmd"}, `\bcmVmd[A-Za-z0-9]{59}\b`, 0},
	{"aws-amazon-bedrock-api-key-long-lived", []string{"absk"}, `\bABSK[A-Za-z0-9+/]{109,}={0,2}`, 0},
	// The short-lived key is a presigned request whose fixed first bytes
	// spell "bedrock.amazonaws.com"; the rest of it is the credential.
	{"aws-amazon-bedrock-api-key-short-lived", []string{"bedrock-api-key-"}, `bedrock-api-key-YmVkcm9jay5hbWF6b25hd3MuY29t[A-Za-z0-9+/=%_-]*`, 0},
	{"authress-service-client-access-key", []string{"sc_", "ext_", "scauth_", "authress_"}, `\b(?:sc|ext|scauth|authress)_(?i:[a-z0-9]{5,30}\.[a-z0-9]{4,6}\.)acc[_-](?i:[a-z0-9-]{10,32}\.[a-z0-9+/_=-]{30,120})`, 0},
	{"clickhouse-cloud-api-secret-key", []string{"4b1d"}, `\b4b1d[A-Za-z0-9]{38}\b`, 0},
	{"databricks-api-token", []string{"dapi"}, `\bdapi[a-f0-9]{32}(?:-\d)?\b`, 0},
	{"facebook-page-access-token", []string{"eaam", "eaac"}, `\bEAA[MC][a-zA-Z0-9]{100,}`, 0},
	{"flutterwave-encryption-key", []string{"flwseck_test"}, `\bFLWSECK_TEST-(?i:[a-h0-9]{12})\b`, 0},
	{"flutterwave-public-key", []string{"flwpubk_test"}, `\bFLWPUBK_TEST-(?i:[a-h0-9]{32})-X\b`, 0},
	{"grafana-api-key", []string{"eyjrijoi"}, `\beyJrIjoi[A-Za-z0-9]{70,}={0,3}`, 0},
	{"harness-api-key", []string{"pat.", "sat."}, `\b(?:pat|sat)\.[a-zA-Z0-9_-]{22}\.[a-zA-Z0-9]{24}\.[a-zA-Z0-9]{20}\b`, 0},
	{"intra42-client-secret", []string{"s-s4t2ud-", "s-s4t2af-"}, `\bs-s4t2(?:ud|af)-(?i:[a-f0-9]{64})\b`, 0},
	{"maxmind-license-key", []string{"_mmk"}, `\b[A-Za-z0-9]{6}_[A-Za-z0-9]{29}_mmk\b`, 0},
	{"microsoft-teams-webhook", []string{"webhook.office.com"}, `(?i)https://[a-z0-9]+\.webhook\.office\.com/webhookb2/[a-z0-9]{8}-(?:[a-z0-9]{4}-){3}[a-z0-9]{12}@[a-z0-9]{8}-(?:[a-z0-9]{4}-){3}[a-z0-9]{12}/IncomingWebhook/[a-z0-9]{32}/[a-z0-9]{8}-(?:[a-z0-9]{4}-){3}[a-z0-9]{12}`, 0},
	{"settlemint-application-access-token", []string{"sm_aat_"}, `\bsm_aat_[a-zA-Z0-9]{16}\b`, 0},
	{"settlemint-personal-access-token", []string{"sm_pat_"}, `\bsm_pat_[a-zA-Z0-9]{16}\b`, 0},
	{"settlemint-service-access-token", []string{"sm_sat_"}, `\bsm_sat_[a-zA-Z0-9]{16}\b`, 0},
	// Only the sgp_ forms: upstream's bare 40-hex alternative is a git hash.
	{"sourcegraph-access-token", []string{"sgp_"}, `\bsgp_(?:(?:[a-fA-F0-9]{16}|local)_)?[a-fA-F0-9]{40}\b`, 0},
	{"mapbox-api-token", []string{"mapbox"}, ctx("mapbox[\\w-]*") + `(pk\.[a-z0-9]{60}\.[a-z0-9]{22})\b`, 1},
	{"twitter-bearer-token", []string{"twitter"}, ctx("twitter[\\w-]*") + `(A{22}[a-z0-9%]{80,100})\b`, 1},
	{"freemius-secret-key", []string{"secret_key"}, `(?i)["']secret_key["']\s*=>\s*["'](sk_[^\s"']{29})["']`, 1},
	{"nuget-config-password", []string{"<add key="}, `(?i)<add key="(?:ClearText)?Password"\s*value="([^"]{8,})"\s*/>`, 1},
	// Context-keyed hex tokens
	{"twilio-account-sid", []string{"twilio"}, ctx("twilio[\\w-]*") + `(AC[0-9a-f]{32})\b`, 1},
}

type compiledRule struct {
	tokenRule
	re *regexp.Regexp
}

var compiledRules = sync.OnceValue(func() []compiledRule {
	out := make([]compiledRule, len(tokenRules))
	for i, r := range tokenRules {
		// MustCompile: a rule that does not compile is a bug in this file, and
		// failing every mask at startup is better than masking with fewer
		// rules than the build claims (invariant: a failure is reported).
		out[i] = compiledRule{r, regexp.MustCompile(r.pattern)}
	}
	return out
})

// maskTokenRules masks every provider-shaped secret in one line. hits is which
// rules' keywords the line holds. A match that already holds the marker is
// left alone, as replaceCounting does: a checkpoint masks its folded notes a
// second time.
func maskTokenRules(line string, hits *hitSet, field string, found *[]Redaction) string {
	for i, r := range compiledRules() {
		if !hits.has(i) {
			continue
		}
		locs := findRuleMatches(r, line)
		if len(locs) == 0 {
			continue
		}
		var b strings.Builder
		last := 0
		for _, m := range locs {
			s, e := m[0], m[1]
			if r.group > 0 {
				s, e = m[2*r.group], m[2*r.group+1]
			}
			if s < last || s < 0 || strings.Contains(line[s:e], Marker) {
				continue
			}
			b.WriteString(line[last:s])
			b.WriteString(Marker)
			last = e
			*found = append(*found, Redaction{Field: field, Reason: "known credential shape (" + r.id + ")"})
		}
		if last == 0 {
			continue
		}
		b.WriteString(line[last:])
		line = b.String()
	}
	return line
}

// hexKeywords is prefix followed by each hex digit: the literal a rule such as
// SK<hex> is woken by, longer than "sk" so that "task" and "risk" do not wake it.
func hexKeywords(prefix, suffix string) []string {
	var out []string
	for _, d := range "0123456789abcdef" {
		out = append(out, prefix+string(d)+suffix)
	}
	return out
}

// digitKeywords is prefix followed by each decimal digit.
func digitKeywords(prefix string) []string {
	var out []string
	for _, d := range "0123456789" {
		out = append(out, prefix+string(d))
	}
	return out
}

// findRuleMatches returns the submatch indices of every match of r. A rule
// that masks a capture group resumes scanning at the end of the group, not of
// the match: azure-ad-client-secret consumes its trailing delimiter, which is
// the next secret's leading one, so "SECRET1 SECRET2" masked only the first.
// The slice then begins at that delimiter, which its own "^" alternative accepts.
func findRuleMatches(r compiledRule, line string) [][]int {
	if r.group == 0 {
		return r.re.FindAllStringSubmatchIndex(line, -1)
	}
	var out [][]int
	pos := 0
	for pos <= len(line) {
		m := r.re.FindStringSubmatchIndex(line[pos:])
		if m == nil {
			break
		}
		for i := range m {
			if m[i] >= 0 {
				m[i] += pos
			}
		}
		out = append(out, m)
		ge := m[2*r.group+1]
		if ge <= pos {
			ge = pos + 1
		}
		pos = ge
	}
	return out
}
