package confmigrate

// migrateTo32 performs schema version update from 31 to 32.
// This migration adds dns.cache_optimistic_answer_ttl and
// dns.cache_optimistic_max_age fields for optimistic DNS cache control.
func migrateTo32(diskConf yobj) (err error) {
	diskConf["schema_version"] = 32

	return nil
}
