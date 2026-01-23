package confmigrate

// migrateTo28 performs schema version update.
// This is a compatibility migration to support configurations
// created by newer AdGuard Home versions.
func migrateTo28(diskConf yobj) (err error) {
	diskConf["schema_version"] = 28

	return nil
}
