package confmigrate

// migrateTo29 performs schema version update.
// This is a compatibility migration to support configurations
// created by newer AdGuard Home versions.
func migrateTo29(diskConf yobj) (err error) {
	diskConf["schema_version"] = 29

	return nil
}
