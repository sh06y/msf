# Deterministic DNS fixture

This fixture provides stable records for the MosDNS IPv4/IPv6 preference matrix and listens on both UDP and TCP.

```bash
python3 tests/fixtures/dns/dns_fixture.py --host 127.0.0.1 --port 15353
npm run test:dns-fixture
```

| Name | A | AAAA |
|---|---|---|
| `dual.test` | `192.0.2.10` | `2001:db8::10` |
| `v4-only.test` | `192.0.2.20` | NODATA |
| `v6-only.test` | NODATA | `2001:db8::20` |
| `cname-dual.test` | CNAME to `dual.test` | CNAME to `dual.test` |
| `no-address.test` | NODATA | NODATA |

Unknown names return NXDOMAIN. The fixture uses documentation-only address ranges and does not contact public DNS.

## MosDNS routing-memory regression

The Go regression uses real MosDNS sequences and deterministic upstream plugins;
no public DNS or production service is used. It checks both modes, A/AAAA,
subscription precedence over stale/conflicting memory, failed/empty answers,
and persistence after replacing or clearing a running core's memory files.

```bash
MSF_RUNTIME=native MSF_TEST_MOSDNS=/absolute/path/to/mosdns \
  go test ./internal/server -run 'TestMosDNSLearning|TestMosDNSFakeIPParsing' -count=1
```

Validated with `yyysuo/mosdns` commit
`b93784b95b1a827ef684e42636b3e16ab4e04be1`. Without `MSF_TEST_MOSDNS`,
the real-core tests are skipped; classification and migration tests still run.
