import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, fireEvent, screen } from '@solidjs/testing-library';

const { themeMock, mocks } = vi.hoisted(() => {
    const proxy: any = new Proxy(
        {},
        {
            get: (_target, prop) => {
                if (prop === Symbol.toPrimitive || prop === 'toString') {
                    return () => '';
                }
                return proxy;
            },
        },
    );
    return {
        themeMock: proxy,
        mocks: {
            dnsInfo: vi.fn(),
            dnsConfig: vi.fn(),
            cacheClear: vi.fn(),
        },
    };
});

vi.mock('panel/lib/theme', () => ({
    default: themeMock,
}));

vi.mock('panel/common/intl', () => ({
    default: {
        getMessage: (key: string, _values?: Record<string, unknown>) => key,
        getPlural: (key: string) => key,
    },
}));

vi.mock('panel/api/generated', () => ({
    dnsInfo: mocks.dnsInfo,
    dnsConfig: mocks.dnsConfig,
    cacheClear: mocks.cacheClear,
}));

vi.mock('panel/stores/toasts', () => ({
    addErrorToast: vi.fn(),
    addSuccessToast: vi.fn(),
}));

import { Cache } from 'panel/components/DnsSettings/Cache/Cache';
import { getDnsConfig } from 'panel/stores/dnsConfig';

const REFRESH_ROW_TITLES = [
    'dns_cache_refresh_max_concurrent',
    'dns_cache_refresh_delay',
    'dns_cache_refresh_min_hits',
    'dns_cache_refresh_prefetch_top',
    'dns_cache_refresh_prefetch_before',
    'dns_cache_refresh_max_tracked',
];

const loadConfig = async (overrides: Record<string, unknown>) => {
    mocks.dnsInfo.mockResolvedValue({
        upstream_mode: '',
        cache_enabled: true,
        cache_size: 4194304,
        cache_ttl_min: 0,
        cache_ttl_max: 0,
        cache_optimistic: true,
        cache_refresh_enabled: false,
        cache_refresh_max_concurrent: 4,
        cache_refresh_delay: 1,
        cache_refresh_min_hits: 3,
        cache_refresh_prefetch_top: 100,
        cache_refresh_prefetch_before: 10,
        cache_refresh_max_tracked: 10000,
        ...overrides,
    });
    await getDnsConfig();
};

// Closed dialogs reuse the row titles, so look for the titles only inside the
// setting rows.
const hasRow = (title: string) =>
    Array.from(document.querySelectorAll('[role="button"]')).some((row) =>
        Array.from(row.querySelectorAll('div')).some((el) => el.textContent === title),
    );

const refreshSwitchRow = () =>
    screen.getByText('dns_cache_refresh').closest('[role="button"]') as HTMLElement;

describe('Cache proactive refresh settings', () => {
    beforeEach(() => vi.clearAllMocks());

    it('disables the refresh switch without optimistic caching', async () => {
        await loadConfig({ cache_optimistic: false, cache_refresh_enabled: true });
        render(() => <Cache />);

        fireEvent.click(refreshSwitchRow());
        expect(mocks.dnsConfig).not.toHaveBeenCalled();

        for (const title of REFRESH_ROW_TITLES) {
            expect(hasRow(title)).toBe(false);
        }
    });

    it('hides the refresh settings while refresh is off', async () => {
        await loadConfig({ cache_refresh_enabled: false });
        render(() => <Cache />);

        for (const title of REFRESH_ROW_TITLES) {
            expect(hasRow(title)).toBe(false);
        }
    });

    it('shows the refresh settings and toggles refresh', async () => {
        mocks.dnsConfig.mockResolvedValue({});
        await loadConfig({ cache_refresh_enabled: true });
        render(() => <Cache />);

        for (const title of REFRESH_ROW_TITLES) {
            expect(hasRow(title)).toBe(true);
        }
        expect(screen.getByText('10,000')).toBeDefined();

        fireEvent.click(refreshSwitchRow());
        expect(mocks.dnsConfig).toHaveBeenCalledWith({ cache_refresh_enabled: false });
    });
});
