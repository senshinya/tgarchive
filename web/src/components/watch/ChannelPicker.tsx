import { Link2, Megaphone, RefreshCw } from 'lucide-preact';
import { useEffect, useMemo, useRef, useState } from 'preact/hooks';
import { ApiError, avatarUrl, errorMessage } from '../../api/client';
import type { ChannelInfo } from '../../api/types';
import { navigate } from '../../lib/router';
import { formatCount } from '../../lib/watchCond';
import { useStore } from '../../state/store';
import { Avatar } from '../../ui/Avatar';
import { IconButton } from '../../ui/Button';
import { InputField } from '../../ui/InputField';
import { ListItem } from '../../ui/ListItem';
import { Spinner } from '../../ui/Spinner';
import { Description, Section } from '../settings/common';
import './watch.scss';

const LINKISH = /^(@|https?:\/\/|t\.me\/|telegram\.me\/)/i;
const SEARCH_DELAY = 500;
/** How often the picker re-reads the list while the server is still scanning the account. */
export const SCAN_POLL_MS = 3000;

function matches(c: ChannelInfo, q: string): boolean {
  const n = q.toLowerCase().replace(/^@/, '');
  return c.title.toLowerCase().includes(n) || c.username.toLowerCase().includes(n);
}

function ChannelRow({ c, onPick }: { c: ChannelInfo; onPick: (c: ChannelInfo) => void }) {
  const parts = [c.username ? `@${c.username}` : '私有频道'];
  if (c.participants > 0) parts.push(`${formatCount(c.participants)} 订阅`);
  return (
    <button type="button" class="ChannelRow" onClick={() => onPick(c)}>
      <Avatar name={c.title} peerId={c.channel_id} src={avatarUrl('channels', c.channel_id)} size="medium" />
      <span class="ListItem-text">
        <span class="ListItem-title">{c.title}</span>
        <span class="ListItem-subtitle">{parts.join(' · ')}</span>
      </span>
      {c.watched && <span class="ChannelRow-badge">已监听</span>}
    </button>
  );
}

/** Step one of adding a watch: the account's channels, filtered as you type; public search when
 * nothing matches; or a pasted @name / t.me link (spec §6). */
export function ChannelPicker({ onPick }: { onPick: (c: ChannelInfo) => void }) {
  const store = useStore();
  const [list, setList] = useState<ChannelInfo[] | null>(null);
  const [scanning, setScanning] = useState(false);
  const [error, setError] = useState('');
  const [offline, setOffline] = useState(false);
  const [q, setQ] = useState('');
  const [found, setFound] = useState<ChannelInfo[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [resolving, setResolving] = useState(false);
  const seq = useRef(0);

  const load = async (refresh: boolean) => {
    try {
      const res = await store.api.channels(refresh);
      setList(res.channels);
      setScanning(res.loading);
      setError(res.loading ? '' : res.error);
      setOffline(false);
    } catch (e) {
      setList((l) => l ?? []);
      setScanning(false);
      if (e instanceof ApiError && e.status === 409) setOffline(true);
      else setError(errorMessage(e));
    }
  };
  useEffect(() => {
    void load(false);
  }, []);
  // The server scans the account's dialogs in the background (Telegram throttles it): keep
  // re-reading until it is done.
  useEffect(() => {
    if (!scanning) return;
    const t = setTimeout(() => void load(false), SCAN_POLL_MS);
    return () => clearTimeout(t);
  }, [scanning, list]);

  const query = q.trim();
  const linkish = LINKISH.test(query);
  const local = useMemo(() => (list && query ? list.filter((c) => matches(c, query)) : list ?? []), [list, query]);
  const wantSearch = !linkish && query.length >= 2 && local.length === 0 && !offline;

  useEffect(() => {
    setFound(null);
    if (!wantSearch) return;
    const my = ++seq.current;
    setSearching(true);
    const t = setTimeout(async () => {
      try {
        const res = await store.api.searchChannels(query);
        if (my === seq.current) setFound(res);
      } catch (e) {
        if (my === seq.current) store.showToast(errorMessage(e));
      } finally {
        if (my === seq.current) setSearching(false);
      }
    }, SEARCH_DELAY);
    return () => {
      clearTimeout(t);
      if (my === seq.current) setSearching(false);
    };
  }, [query, wantSearch]);

  const resolve = async () => {
    setResolving(true);
    try {
      onPick(await store.api.resolveChannel(query));
    } catch (e) {
      store.showToast(errorMessage(e));
    } finally {
      setResolving(false);
    }
  };

  return (
    <>
      <Section>
        <div class="ChannelPicker-search">
          <InputField label="搜索频道，或粘贴 @用户名 / t.me 链接" value={q} onInput={setQ} autoFocus />
          <IconButton label="刷新频道列表" onClick={() => void load(true)} disabled={offline || scanning}>
            <RefreshCw size={20} />
          </IconButton>
        </div>
        {offline && (
          <Description>
            代取账号未登录，无法读取频道。
            <button type="button" class="link-button" onClick={() => navigate({ name: 'settings-userbot' })}>
              去登录
            </button>
          </Description>
        )}
        {error && <Description>{error}</Description>}
        {query.startsWith('https://t.me/+') || query.startsWith('t.me/+') ? (
          <Description>私有频道的邀请链接无法直接监听，请先用代取账号加入该频道。</Description>
        ) : (
          linkish && (
            <ListItem icon={resolving ? <Spinner size={24} /> : <Link2 size={24} />} title={`解析 ${query}`} onClick={() => void resolve()} />
          )
        )}
      </Section>
      <Section title="已加入的频道">
        {(list === null || scanning) && (
          <div class="ChannelPicker-scanning">
            <Spinner size={24} />
            <span>{list?.length ? `正在读取频道列表…（已找到 ${list.length} 个）` : '正在读取频道列表…'}</span>
          </div>
        )}
        {list !== null && !scanning && local.length === 0 && !offline && (
          <Description>{query ? '没有匹配的已加入频道' : '代取账号还没有加入任何频道'}</Description>
        )}
        {local.map((c) => (
          <ChannelRow key={c.channel_id} c={c} onPick={onPick} />
        ))}
      </Section>
      {wantSearch && (
        <Section title="公开频道">
          {searching && (
            <div class="settings-loading">
              <Spinner size={32} />
            </div>
          )}
          {!searching && found?.length === 0 && <Description>没有找到公开频道</Description>}
          {found?.map((c) => (
            <ChannelRow key={c.channel_id} c={c} onPick={onPick} />
          ))}
        </Section>
      )}
      {list !== null && list.length === 0 && !scanning && !query && !offline && (
        <Section>
          <ListItem icon={<Megaphone size={24} />} title="提示" subtitle="输入频道名称搜索公开频道，或直接粘贴链接" />
        </Section>
      )}
    </>
  );
}
