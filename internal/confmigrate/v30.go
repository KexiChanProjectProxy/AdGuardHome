package confmigrate

// migrateTo30 performs schema version update from 29 to 30.
// This migration adds the dns.cache_enabled field to explicitly control
// whether DNS caching is enabled, replacing the implicit logic based on cache_size.
func migrateTo30(diskConf yobj) (err error) {
	diskConf["schema_version"] = 30

	return nil
}
