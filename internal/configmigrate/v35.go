package configmigrate

import "context"

// migrateTo35 performs the following changes:
//
//	# BEFORE:
//	'dns':
//	  'cache_optimistic_max_age': '12h'
//	  # …
//
//	# AFTER:
//	'dns':
//	  'cache_optimistic_max_age': '12h'
//	  'cache_refresh_enabled': false
//	  'cache_refresh_max_concurrent': 4
//	  'cache_refresh_delay': '1s'
//	  'cache_refresh_min_hits': 3
//	  'cache_refresh_prefetch_top': 100
//	  'cache_refresh_prefetch_before': '10s'
//	  'cache_refresh_max_tracked': 10000
//	  # …
func (m Migrator) migrateTo35(_ context.Context, diskConf yobj) (err error) {
	diskConf["schema_version"] = 35

	dnsConf, ok, err := fieldVal[yobj](diskConf, "dns")
	if !ok {
		return err
	}

	dnsConf["cache_refresh_enabled"] = false
	dnsConf["cache_refresh_max_concurrent"] = 4
	dnsConf["cache_refresh_delay"] = "1s"
	dnsConf["cache_refresh_min_hits"] = 3
	dnsConf["cache_refresh_prefetch_top"] = 100
	dnsConf["cache_refresh_prefetch_before"] = "10s"
	dnsConf["cache_refresh_max_tracked"] = 10_000

	return nil
}
