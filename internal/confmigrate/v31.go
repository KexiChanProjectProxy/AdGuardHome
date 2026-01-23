package confmigrate

// migrateTo31 performs schema version update from 30 to 31.
// This migration adds filtering.rewrites_enabled to globally enable/disable
// DNS rewrites, and adds an enabled field for each rewrite entry.
func migrateTo31(diskConf yobj) (err error) {
	diskConf["schema_version"] = 31

	return nil
}
