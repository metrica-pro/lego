---
title: "Cloud.ru Evolution DNS"
date: 2019-03-03T16:39:46+01:00
draft: false
slug: cloudruevolution
dnsprovider:
  since:    "v5.1.0"
  code:     "cloudruevolution"
  url:      "https://cloud.ru/products/evolution-dns"
---

<!-- THIS DOCUMENTATION IS AUTO-GENERATED. PLEASE DO NOT EDIT. -->
<!-- providers/dns/cloudruevolution/cloudruevolution.toml -->
<!-- THIS DOCUMENTATION IS AUTO-GENERATED. PLEASE DO NOT EDIT. -->

Cloud.ru Evolution DNS public-zone provider (https://dns.api.cloud.ru). Use this for new Evolution DNS deployments; the legacy `cloudru` provider targets the older console.cloud.ru/api/clouddns API.


<!--more-->

- Code: `cloudruevolution`
- Since: v5.1.0


Here is an example bash command using the Cloud.ru Evolution DNS provider:

```bash
CLOUDRU_EVOLUTION_KEY_ID=xxx \
CLOUDRU_EVOLUTION_SECRET=yyy \
CLOUDRU_EVOLUTION_PROJECT_ID=zzz \
lego --email you@example.com --dns cloudruevolution -d '*.example.com' -d example.com run
```




## Credentials

| Environment Variable Name | Description |
|-----------------------|-------------|
| `CLOUDRU_EVOLUTION_KEY_ID` | IAM API key ID |
| `CLOUDRU_EVOLUTION_PROJECT_ID` | Cloud.ru project UUID |
| `CLOUDRU_EVOLUTION_SECRET` | IAM API key secret |

The environment variable names can be suffixed by `_FILE` to reference a file instead of a value.
More information [here]({{% ref "dns#configuration-and-credentials" %}}).


## Additional Configuration

| Environment Variable Name | Description |
|--------------------------------|-------------|
| `CLOUDRU_EVOLUTION_API_ENDPOINT` | Override the DNS API base URL (Default: https://dns.api.cloud.ru) |
| `CLOUDRU_EVOLUTION_AUTH_ENDPOINT` | Override the IAM token endpoint (Default: https://iam.api.cloud.ru/api/v1/auth/token) |
| `CLOUDRU_EVOLUTION_HTTP_TIMEOUT` | API request timeout in seconds (Default: 30) |
| `CLOUDRU_EVOLUTION_OPERATION_TIMEOUT` | Maximum waiting time for async API operations in seconds (Default: 300) |
| `CLOUDRU_EVOLUTION_POLLING_INTERVAL` | Time between DNS propagation checks in seconds (Default: 5) |
| `CLOUDRU_EVOLUTION_PROPAGATION_TIMEOUT` | Maximum waiting time for DNS propagation in seconds (Default: 300) |
| `CLOUDRU_EVOLUTION_SEQUENCE_INTERVAL` | Time between sequential DNS-01 challenges in seconds (Default: 60) |
| `CLOUDRU_EVOLUTION_TTL` | The TTL of the TXT record used for the DNS challenge in seconds (Default: 120) |

The environment variable names can be suffixed by `_FILE` to reference a file instead of a value.
More information [here]({{% ref "dns#configuration-and-credentials" %}}).




## More information

- [API documentation](https://cloud.ru/docs/evolution-dns/ug/index.html)

<!-- THIS DOCUMENTATION IS AUTO-GENERATED. PLEASE DO NOT EDIT. -->
<!-- providers/dns/cloudruevolution/cloudruevolution.toml -->
<!-- THIS DOCUMENTATION IS AUTO-GENERATED. PLEASE DO NOT EDIT. -->
