import type { DNSConfigBlockingMode } from './dNSConfigBlockingMode';
import type { DNSConfigUpstreamMode } from './dNSConfigUpstreamMode';

/**
 * DNS server configuration
 */
export interface DNSConfig {
    /** Bootstrap servers, port is optional after colon.  Empty value will reset it to default values.  Comments are allowed, a comment line starts with a `#` symbol. */
    bootstrap_dns?: string[];
    /** Upstream servers, port is optional after colon.  Empty value will reset it to default values.  Comments are allowed, a comment line starts with a `#` symbol. */
    upstream_dns?: string[];
    /** List of fallback DNS servers used when upstream DNS servers are not responding.  Empty value will clear the list.  Comments are allowed, a comment line starts with a `#` symbol. */
    fallback_dns?: string[];
    upstream_dns_file?: string;
    protection_enabled?: boolean;
    ratelimit?: number;
    /**
     * Length of the subnet mask for IPv4 addresses.
     * @minimum 0
     * @maximum 32
     */
    ratelimit_subnet_len_ipv4?: number;
    /**
     * Length of the subnet mask for IPv6 addresses.
     * @minimum 0
     * @maximum 128
     */
    ratelimit_subnet_len_ipv6?: number;
    /** List of IP addresses excluded from rate limiting. */
    ratelimit_whitelist?: string[];
    blocking_mode?: DNSConfigBlockingMode;
    blocking_ipv4?: string;
    blocking_ipv6?: string;
    /**
     * TTL for blocked responses.
     * @minimum 0
     */
    blocked_response_ttl?: number;
    /** Protection is pause until this time.  Nullable. */
    protection_disabled_until?: string;
    edns_cs_enabled?: boolean;
    edns_cs_use_custom?: boolean;
    edns_cs_custom_ip?: string;
    disable_ipv6?: boolean;
    dnssec_enabled?: boolean;
    cache_size?: number;
    cache_ttl_min?: number;
    cache_ttl_max?: number;
    /**
     * Enables or disables the DNS response cache.
     *
     * If `cache_enabled` is `true`, the companion field `cache_size` must
     * be present and greater than 0, or the `dns.cache_size` setting in
     * the configuration file must already be greater than 0.
     */
    cache_enabled?: boolean;
    cache_optimistic?: boolean;
    /** Enables the proactive refresh of popular cache entries.  It only has effect when both `cache_enabled` and `cache_optimistic` are `true`. */
    cache_refresh_enabled?: boolean;
    /**
     * Maximum number of simultaneous proactive refreshes.
     * @minimum 1
     * @maximum 1024
     */
    cache_refresh_max_concurrent?: number;
    /**
     * How long after its expiry a cache entry is refreshed, in seconds. Until then, the expired entry is served optimistically.  It must be less than the `dns.cache_optimistic_max_age` setting of the configuration file.
     * @minimum 0
     */
    cache_refresh_delay?: number;
    /**
     * Only the cache entries queried more than this number of times in the last 24 hours are refreshed.
     * @minimum 0
     */
    cache_refresh_min_hits?: number;
    /**
     * Number of the most queried cache entries that are refreshed before their expiry instead of after it.
     * @minimum 0
     */
    cache_refresh_prefetch_top?: number;
    /**
     * How long before its expiry one of the most queried cache entries is refreshed, in seconds.  The window is limited to the half of the entry's TTL.
     * @minimum 0
     */
    cache_refresh_prefetch_before?: number;
    /**
     * Maximum number of tracked cache keys.  The least recently queried keys are evicted beyond it.
     * @minimum 1
     * @maximum 100000
     */
    cache_refresh_max_tracked?: number;
    /** Upstream modes enumeration. The empty string value is deprecated; use `load_balance` instead. */
    upstream_mode?: DNSConfigUpstreamMode;
    use_private_ptr_resolvers?: boolean;
    resolve_clients?: boolean;
    /** Upstream servers, port is optional after colon.  Empty value will reset it to default values.  Comments are allowed, a comment line starts with a `#` symbol. */
    local_ptr_upstreams?: string[];
    /**
     * The number of seconds to wait for a response from the upstream server
     * @minimum 1
     */
    upstream_timeout?: number;
}
