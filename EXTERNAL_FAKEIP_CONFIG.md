# External FakeIP Configuration File Support

## Feature: Reference External YAML for FakeIP Config

This feature allows AdGuard Home to reference an external YAML file for FakeIP configuration instead of inline configuration.

## Usage

### Option 1: Inline Configuration (Original)
```yaml
dns:
  fakeip_enabled: true
  fakeip_from:
    - "127.0.0.0/8"
    - "10.0.0.0/8"
  fakeip_domain_suffix:
    - "test.local"
  fakeip_ipv4_range: "192.18.0.0/16"
  fakeip_ipv6_range: "2001:0db8:114::/48"
  fakeip_ttl: 30
```

### Option 2: External Configuration File (New)
**AdGuardHome.yaml:**
```yaml
dns:
  fakeip_config_file: "/path/to/fakeip-config.yaml"
```

**fakeip-config.yaml:**
```yaml
enabled: true
from:
  - "127.0.0.0/8"
  - "10.0.0.0/8"
  - "192.168.0.0/16"
domain_suffix:
  - "test.local"
  - "example.internal"
  - "*.dev"
ipv4_range: "192.18.0.0/16"
ipv6_range: "2001:0db8:114::/48"
ttl: 30
```

## Benefits

1. **Separation of Concerns**: Keep FakeIP config separate from main AdGuard Home config
2. **Easier Management**: Edit FakeIP settings without touching main config
3. **Reusability**: Share same FakeIP config across multiple AdGuard Home instances
4. **Version Control**: Track FakeIP changes separately
5. **Complex Configurations**: Support large domain/IP lists without cluttering main config

## Implementation

### Configuration Structure

Add to `internal/dnsforward/config.go`:
```go
// FakeIPConfigFile is the path to external FakeIP configuration file.
// If set, this takes precedence over inline fakeip_* settings.
FakeIPConfigFile string `yaml:"fakeip_config_file"`
```

### External Config File Format

```go
type FakeIPExternalConfig struct {
	Enabled       bool     `yaml:"enabled"`
	From          []string `yaml:"from"`
	DomainSuffix  []string `yaml:"domain_suffix"`
	IPv4Range     string   `yaml:"ipv4_range"`
	IPv6Range     string   `yaml:"ipv6_range"`
	TTL           uint32   `yaml:"ttl"`
}
```

### Loading Logic

```go
func (s *Server) loadFakeIPConfig() (*proxy.FakeIPConfig, error) {
	srvConf := s.conf

	// Check if external config file is specified
	if srvConf.FakeIPConfigFile != "" {
		return s.loadFakeIPFromFile(srvConf.FakeIPConfigFile)
	}

	// Fall back to inline configuration
	if !srvConf.FakeIPEnabled {
		return nil, nil
	}

	return s.buildFakeIPConfigFromInline()
}

func (s *Server) loadFakeIPFromFile(path string) (*proxy.FakeIPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading fakeip config file %q: %w", path, err)
	}

	var extConf FakeIPExternalConfig
	err = yaml.Unmarshal(data, &extConf)
	if err != nil {
		return nil, fmt.Errorf("parsing fakeip config file %q: %w", path, err)
	}

	if !extConf.Enabled {
		log.Info("dnsforward: fakeip disabled in external config file")
		return nil, nil
	}

	return s.buildFakeIPConfigFromExternal(&extConf)
}
```

### Priority Rules

1. If `fakeip_config_file` is set → load from external file (inline settings ignored)
2. If `fakeip_config_file` is empty → use inline `fakeip_*` settings
3. If both are empty → FakeIP disabled

### Validation

- External file must exist and be readable
- YAML must be valid
- Same validation rules as inline config
- Log clear error messages if file is missing/invalid

### Hot Reload Support

Watch the external config file for changes:
```go
// Optional: Add file watcher
if srvConf.FakeIPConfigFile != "" {
	go s.watchFakeIPConfigFile(srvConf.FakeIPConfigFile)
}
```

## Example Use Cases

### Use Case 1: Development Environment
```yaml
# fakeip-dev.yaml
enabled: true
from:
  - "127.0.0.1/32"
  - "::1/128"
domain_suffix:
  - "*.dev"
  - "*.local"
  - "*.test"
ipv4_range: "240.0.0.0/16"
ipv6_range: "2001:0db8:1234::/48"
ttl: 10  # Short TTL for development
```

### Use Case 2: Corporate Network
```yaml
# fakeip-corporate.yaml
enabled: true
from:
  - "10.0.0.0/8"
  - "172.16.0.0/12"
  - "192.168.0.0/16"
domain_suffix:
  - "*.internal"
  - "*.corp"
  - "*.local"
  # ... hundreds of internal domains
ipv4_range: "100.64.0.0/16"
ipv6_range: "fd00::/48"
ttl: 60
```

### Use Case 3: Testing/CI
```yaml
# fakeip-ci.yaml
enabled: true
from:
  - "0.0.0.0/0"  # Allow from anywhere in test environment
domain_suffix:
  - "*.test"
  - "mock.example.com"
ipv4_range: "198.18.0.0/15"
ipv6_range: "2001:0db8::/32"
ttl: 5  # Very short TTL for rapid iteration
```

## Error Handling

### File Not Found
```
ERROR: fakeip config file not found: /path/to/fakeip.yaml
Falling back to inline configuration or disabling FakeIP
```

### Invalid YAML
```
ERROR: failed to parse fakeip config file: yaml: line 5: invalid syntax
FakeIP disabled
```

### Validation Errors
```
ERROR: fakeip config validation failed: ipv4_range must be specified
FakeIP disabled
```

## Migration Path

### Step 1: Continue Using Inline Config
No changes needed, existing configs work as-is.

### Step 2: Extract to External File
1. Create `fakeip-config.yaml` with your settings
2. Add `fakeip_config_file: "/path/to/fakeip-config.yaml"` to `AdGuardHome.yaml`
3. Remove inline `fakeip_*` settings (optional, they're ignored if external file is set)
4. Restart AdGuard Home

### Step 3: Manage External Config
- Edit `fakeip-config.yaml` as needed
- Optionally implement hot-reload to apply changes without restart

## Implementation Status

- [ ] Add `FakeIPConfigFile` field to ServerConfig
- [ ] Implement `loadFakeIPFromFile()`
- [ ] Implement external YAML parsing
- [ ] Add file existence/permission checks
- [ ] Implement priority logic (external > inline)
- [ ] Add validation for external config
- [ ] Add logging for config source
- [ ] Optional: Implement hot-reload with file watcher
- [ ] Update documentation
- [ ] Add test cases

## Testing

### Test Cases
1. External file specified and valid → loads external config
2. External file specified but missing → error, FakeIP disabled
3. External file invalid YAML → error, FakeIP disabled
4. External file with `enabled: false` → FakeIP disabled
5. No external file, inline config present → loads inline config
6. Neither external nor inline → FakeIP disabled
7. Hot reload: file changes → config reloads (if implemented)

## Notes

- External config file path can be absolute or relative to AdGuard Home working directory
- File permissions must allow AdGuard Home process to read the file
- Changes to external file require restart (unless hot-reload is implemented)
- External config takes 100% precedence - inline settings are completely ignored when external file is specified
