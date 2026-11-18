/**
 * Status of the proactive DNS cache refresh.  The counters are reset when the DNS server is restarted or reconfigured.
 */
export interface CacheRefreshStatus {
    /** Whether the proactive cache refresh is running. */
    enabled: boolean;
    /** Number of refreshes that cached a new response. */
    refreshed: number;
    /** Number of failed refreshes. */
    failed: number;
    /** Number of refreshes that fetched nothing, because the entry was still fresh or was being refreshed elsewhere, or that dropped an entry which became uncacheable. */
    skipped: number;
    /** Number of currently tracked cache keys. */
    tracked: number;
}
