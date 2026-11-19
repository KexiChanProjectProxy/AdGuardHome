/**
 * Status of the proactive DNS cache refresh.  The counters are reset when the DNS server is restarted or reconfigured.  Only the general cache is refreshed, so responses cached per client subnet or for clients with custom upstreams aren't tracked.
 */
export interface CacheRefreshStatus {
    /** Whether the proactive cache refresh is running. */
    enabled: boolean;
    /**
     * Number of refreshes that cached a new response.
     * @minimum 0
     */
    refreshed: number;
    /**
     * Number of failed refreshes, including upstream SERVFAIL answers, which don't replace the cached response.
     * @minimum 0
     */
    failed: number;
    /**
     * Number of refreshes that fetched nothing, because the entry was still fresh or was being refreshed elsewhere, or that dropped an entry which became uncacheable.
     * @minimum 0
     */
    skipped: number;
    /**
     * Number of currently tracked cache keys.
     * @minimum 0
     */
    tracked: number;
}
