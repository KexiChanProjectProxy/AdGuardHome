import { createSignal, createMemo, Show } from 'solid-js';
import cn from 'clsx';

import {
    dnsConfigState,
    clearDnsCache,
    toggleCacheEnabled,
    toggleOptimisticCaching,
    toggleCacheRefreshEnabled,
} from 'panel/stores/dnsConfig';
import intl from 'panel/common/intl';
import { Button } from 'panel/common/ui/Button';
import { SettingRow } from 'panel/common/ui/SettingRow';
import { ConfirmDialog } from 'panel/common/ui/ConfirmDialog';
import { useDialog } from 'panel/hooks/useDialog';
import { getCacheSizeSummary, getTtlSummary } from '../helpers';
import theme from 'panel/lib/theme';

import s from './Cache.module.pcss';
import { CacheInputDialog } from './blocks/CacheInputDialog';

export const Cache = () => {
    const [showClearConfirm, setShowClearConfirm] = createSignal(false);

    const cacheSizeDialog = useDialog();
    const minTtlDialog = useDialog();
    const maxTtlDialog = useDialog();
    const refreshMaxConcurrentDialog = useDialog();
    const refreshDelayDialog = useDialog();
    const refreshMinHitsDialog = useDialog();
    const refreshPrefetchTopDialog = useDialog();
    const refreshPrefetchBeforeDialog = useDialog();
    const refreshMaxTrackedDialog = useDialog();

    const cacheSizeValue = createMemo(() => getCacheSizeSummary(dnsConfigState.cache_size));
    const minTtlValue = createMemo(() => getTtlSummary(dnsConfigState.cache_ttl_min));
    const maxTtlValue = createMemo(() => getTtlSummary(dnsConfigState.cache_ttl_max));

    const processing = () => dnsConfigState.processingSetConfig;

    // Proactive refresh only works on top of the optimistic cache.
    const refreshAvailable = () => !!dnsConfigState.cache_enabled && !!dnsConfigState.cache_optimistic;
    const refreshSettingsDisabled = () => !refreshAvailable() || !dnsConfigState.cache_refresh_enabled;
    const countSummary = (value?: number) => (value === undefined ? '' : String(value));

    return (
        <div class={s.section}>
            <SettingRow
                variant="switch"
                id="cache_enabled"
                title={intl.getMessage('dns_cache_title')}
                titleClass={cn(theme.title.h5, theme.title.h4_tablet, theme.text.bold, s.title)}
                descriptionClass={s.description}
                align="center"
                description={intl.getMessage('dns_cache_desc')}
                checked={!!dnsConfigState.cache_enabled}
                onChange={() => toggleCacheEnabled()}
            />

            <SettingRow
                variant="link"
                id="cache_size"
                title={intl.getMessage('dns_cache_size')}
                description={intl.getMessage('dns_cache_size_desc')}
                value={cacheSizeValue()}
                disabled={!dnsConfigState.cache_enabled}
                onClick={cacheSizeDialog.openDialog}
            />

            <SettingRow
                variant="link"
                id="override_min_ttl"
                title={intl.getMessage('dns_override_min_ttl')}
                description={intl.getMessage('dns_override_min_ttl_desc')}
                value={minTtlValue()}
                disabled={!dnsConfigState.cache_enabled}
                onClick={minTtlDialog.openDialog}
            />

            <SettingRow
                variant="link"
                id="override_max_ttl"
                title={intl.getMessage('dns_override_max_ttl')}
                description={intl.getMessage('dns_override_max_ttl_desc')}
                value={maxTtlValue()}
                disabled={!dnsConfigState.cache_enabled}
                onClick={maxTtlDialog.openDialog}
            />

            <SettingRow
                variant="switch"
                id="optimistic_caching"
                title={intl.getMessage('dns_optimistic_caching')}
                description={intl.getMessage('dns_optimistic_caching_desc')}
                checked={!!dnsConfigState.cache_optimistic}
                disabled={!dnsConfigState.cache_enabled}
                onChange={() => toggleOptimisticCaching()}
            />

            <SettingRow
                variant="switch"
                id="cache_refresh_enabled"
                title={intl.getMessage('dns_cache_refresh')}
                description={intl.getMessage('dns_cache_refresh_desc')}
                checked={!!dnsConfigState.cache_refresh_enabled}
                disabled={!refreshAvailable()}
                onChange={() => toggleCacheRefreshEnabled()}
            />

            <Show when={refreshAvailable() && dnsConfigState.cache_refresh_enabled}>
                <SettingRow
                    variant="link"
                    id="cache_refresh_max_concurrent"
                    title={intl.getMessage('dns_cache_refresh_max_concurrent')}
                    description={intl.getMessage('dns_cache_refresh_max_concurrent_desc')}
                    value={countSummary(dnsConfigState.cache_refresh_max_concurrent)}
                    disabled={refreshSettingsDisabled()}
                    onClick={refreshMaxConcurrentDialog.openDialog}
                />

                <SettingRow
                    variant="link"
                    id="cache_refresh_delay"
                    title={intl.getMessage('dns_cache_refresh_delay')}
                    description={intl.getMessage('dns_cache_refresh_delay_desc')}
                    value={getTtlSummary(dnsConfigState.cache_refresh_delay)}
                    disabled={refreshSettingsDisabled()}
                    onClick={refreshDelayDialog.openDialog}
                />

                <SettingRow
                    variant="link"
                    id="cache_refresh_min_hits"
                    title={intl.getMessage('dns_cache_refresh_min_hits')}
                    description={intl.getMessage('dns_cache_refresh_min_hits_desc')}
                    value={countSummary(dnsConfigState.cache_refresh_min_hits)}
                    disabled={refreshSettingsDisabled()}
                    onClick={refreshMinHitsDialog.openDialog}
                />

                <SettingRow
                    variant="link"
                    id="cache_refresh_prefetch_top"
                    title={intl.getMessage('dns_cache_refresh_prefetch_top')}
                    description={intl.getMessage('dns_cache_refresh_prefetch_top_desc')}
                    value={countSummary(dnsConfigState.cache_refresh_prefetch_top)}
                    disabled={refreshSettingsDisabled()}
                    onClick={refreshPrefetchTopDialog.openDialog}
                />

                <SettingRow
                    variant="link"
                    id="cache_refresh_prefetch_before"
                    title={intl.getMessage('dns_cache_refresh_prefetch_before')}
                    description={intl.getMessage('dns_cache_refresh_prefetch_before_desc')}
                    value={getTtlSummary(dnsConfigState.cache_refresh_prefetch_before)}
                    disabled={refreshSettingsDisabled()}
                    onClick={refreshPrefetchBeforeDialog.openDialog}
                />

                <SettingRow
                    variant="link"
                    id="cache_refresh_max_tracked"
                    title={intl.getMessage('dns_cache_refresh_max_tracked')}
                    description={intl.getMessage('dns_cache_refresh_max_tracked_desc')}
                    value={countSummary(dnsConfigState.cache_refresh_max_tracked)}
                    disabled={refreshSettingsDisabled()}
                    onClick={refreshMaxTrackedDialog.openDialog}
                />
            </Show>

            <div class={theme.form.actionRow}>
                <Button
                    variant="secondary-danger"
                    onClick={() => setShowClearConfirm(true)}
                    class={theme.form.actionButton}
                    compact
                >
                    {intl.getMessage('dns_clear_cache')}
                </Button>
            </div>

            <Show when={showClearConfirm()}>
                <ConfirmDialog
                    title={intl.getMessage('dns_clear_cache_title')}
                    text={intl.getMessage('dns_clear_cache_desc')}
                    buttonText={intl.getMessage('dns_clear_cache_confirm')}
                    cancelText={intl.getMessage('cancel')}
                    buttonVariant="danger"
                    onClose={() => setShowClearConfirm(false)}
                    onConfirm={() => {
                        clearDnsCache();
                        setShowClearConfirm(false);
                    }}
                />
            </Show>

            <CacheInputDialog
                configKey="cache_size"
                open={cacheSizeDialog.open}
                onClose={cacheSizeDialog.closeDialog}
                processing={processing()}
            />

            <CacheInputDialog
                configKey="cache_ttl_min"
                open={minTtlDialog.open}
                onClose={minTtlDialog.closeDialog}
                processing={processing()}
            />

            <CacheInputDialog
                configKey="cache_ttl_max"
                open={maxTtlDialog.open}
                onClose={maxTtlDialog.closeDialog}
                processing={processing()}
            />

            <CacheInputDialog
                configKey="cache_refresh_max_concurrent"
                open={refreshMaxConcurrentDialog.open}
                onClose={refreshMaxConcurrentDialog.closeDialog}
                processing={processing()}
            />

            <CacheInputDialog
                configKey="cache_refresh_delay"
                open={refreshDelayDialog.open}
                onClose={refreshDelayDialog.closeDialog}
                processing={processing()}
            />

            <CacheInputDialog
                configKey="cache_refresh_min_hits"
                open={refreshMinHitsDialog.open}
                onClose={refreshMinHitsDialog.closeDialog}
                processing={processing()}
            />

            <CacheInputDialog
                configKey="cache_refresh_prefetch_top"
                open={refreshPrefetchTopDialog.open}
                onClose={refreshPrefetchTopDialog.closeDialog}
                processing={processing()}
            />

            <CacheInputDialog
                configKey="cache_refresh_prefetch_before"
                open={refreshPrefetchBeforeDialog.open}
                onClose={refreshPrefetchBeforeDialog.closeDialog}
                processing={processing()}
            />

            <CacheInputDialog
                configKey="cache_refresh_max_tracked"
                open={refreshMaxTrackedDialog.open}
                onClose={refreshMaxTrackedDialog.closeDialog}
                processing={processing()}
            />
        </div>
    );
};
