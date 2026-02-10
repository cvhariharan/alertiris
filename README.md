# Alertiris
Send alerts from [alertmanager](https://github.com/prometheus/alertmanager) to [dfir-iris](https://github.com/dfir-iris/iris-web)

## Configuration

Alertiris reads config from `config.toml`

```toml
[server]
listen = ":8080"

[iris]
url = "https://iris.example.com"
api_key = "your-api-key"
skip_tls_verify = false

[db]
path = "./data/badger"

[alerts]
source = "alertmanager"
customer_id = 1                # default customer ID
status_id_new = 2
status_id_resolved = 6
resolved_action = "update"     # "update" or "delete"
default_severity_id = 4

[alerts.severity_map]
critical = 6
high = 5
warning = 4
info = 3

# Route alerts to different IRIS customers by group
[alerts.group_customer_map]
infra = 36
```

## Alertmanager setup

Configure alertiris as a webhook receiver in alertmanager:

```yaml
receivers:
  - name: "iris"
    webhook_configs:
      - url: "http://alertiris:8080/webhook"

  # Route to a specific customer
  - name: "iris-infra"
    webhook_configs:
      - url: "http://alertiris:8080/webhook?group=infra"
```

## Usage

```bash
go build -o alertiris .
./alertiris
```
