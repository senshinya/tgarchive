import { act, fireEvent, screen, waitFor } from '@testing-library/preact';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '../../api/client';
import type { UserbotInfo } from '../../api/types';
import { route } from '../../lib/router';
import { fakeApi, makeBot } from '../../test/fixtures';
import { renderWithStore } from '../../test/render';
import { AddBot } from './AddBot';
import { BotSettings } from './BotSettings';
import { SettingsHome } from './SettingsHome';
import { TelegramAppSettings } from './TelegramAppSettings';
import { USERBOT_POLL_MS, UserbotSettings } from './UserbotSettings';

afterEach(() => {
  route.value = { name: 'home' };
  history.replaceState(null, '', '/');
  vi.useRealTimers();
});

const ub = (state: UserbotInfo['state'], over: Partial<UserbotInfo> = {}): UserbotInfo => ({ state, phone: '', name: '', tg_user_id: 0, error: '', ...over });
const type = (label: string | RegExp, value: string) => fireEvent.input(screen.getByLabelText(label), { target: { value } });

describe('SettingsHome', () => {
  it('lists bots with status and toggles them', async () => {
    const api = fakeApi({
      bots: vi.fn(async () => [
        makeBot({ id: 1, name: 'Alpha', status: 'running' }),
        makeBot({ id: 2, name: 'Beta', status: 'error', last_error: 'Unauthorized' }),
        makeBot({ id: 3, name: 'Gone', status: 'removed' }),
      ]),
      setBotEnabled: vi.fn(async (id: number, enabled: boolean) => makeBot({ id, name: 'Alpha', enabled, status: 'stopped' })),
    });
    const { store } = renderWithStore(<SettingsHome />, api);
    expect(await screen.findByText('Alpha')).toBeTruthy();
    expect(screen.getByText('Unauthorized')).toBeTruthy();
    expect(screen.queryByText('Gone')).toBeNull();
    expect(await screen.findByText('api_id 1 · Bot API 运行中')).toBeTruthy();
    expect(screen.getByText('未登录')).toBeTruthy();
    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: '启用 Alpha' }));
    });
    expect(api.setBotEnabled).toHaveBeenCalledWith(1, false);
    expect(store.bots.value[0].enabled).toBe(false);
    fireEvent.click(screen.getByText('添加机器人'));
    expect(route.value).toEqual({ name: 'settings-add-bot' });
  });

  it('reports a missing userbot endpoint as 未启用', async () => {
    renderWithStore(<SettingsHome />, fakeApi({ userbot: vi.fn(async () => Promise.reject(new ApiError(404, 'not found', null))) }));
    expect(await screen.findByText('未启用')).toBeTruthy();
  });
});

describe('AddBot', () => {
  const TOKEN = '123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456789';

  it('rejects malformed tokens without calling the API', async () => {
    const api = fakeApi();
    renderWithStore(<AddBot />, api);
    type('Bot Token', 'nope');
    fireEvent.click(screen.getByText('添加'));
    expect(await screen.findByLabelText('token 格式不正确')).toBeTruthy();
    expect(api.addBot).not.toHaveBeenCalled();
  });

  it('shows each step after adding', async () => {
    const api = fakeApi({
      addBot: vi.fn(async () => ({
        bot_id: 4,
        steps: [
          { step: 'getMe', ok: true, detail: '@new_bot' },
          { step: 'logOut', ok: true, detail: '已从云端登出' },
          { step: 'start', ok: true, detail: '' },
        ],
      })),
    });
    renderWithStore(<AddBot />, api);
    type('Bot Token', ` ${TOKEN} `);
    await act(async () => {
      fireEvent.click(screen.getByText('添加'));
    });
    expect(api.addBot).toHaveBeenCalledWith(TOKEN);
    expect(screen.getByText('校验 token')).toBeTruthy();
    expect(screen.getByText('@new_bot')).toBeTruthy();
    expect(screen.getByText('开始接收消息')).toBeTruthy();
    fireEvent.click(screen.getByText('设置白名单'));
    expect(route.value).toEqual({ name: 'settings-bot', botId: 4 });
  });

  it('shows the failed step and links an existing bot on conflict', async () => {
    const api = fakeApi({
      addBot: vi.fn(async () =>
        Promise.reject(new ApiError(409, '机器人已存在', { error: '机器人已存在', bot_id: 2, steps: [{ step: 'getMe', ok: true, detail: '@dup' }] })),
      ),
    });
    renderWithStore(<AddBot />, api);
    type('Bot Token', TOKEN);
    await act(async () => {
      fireEvent.click(screen.getByText('添加'));
    });
    expect(await screen.findByLabelText('机器人已存在')).toBeTruthy();
    expect(screen.getByText('@dup')).toBeTruthy();
    fireEvent.click(screen.getByText('查看已有机器人'));
    expect(route.value).toEqual({ name: 'settings-bot', botId: 2 });
  });
});

describe('BotSettings', () => {
  const setup = () => {
    const api = fakeApi({
      bots: vi.fn(async () => [makeBot({ id: 1, name: 'Alpha' })]),
      whitelist: vi.fn(async () => [{ tg_user_id: 42, note: '我', can_fetch: false }]),
      rejected: vi.fn(async () => [
        { tg_user_id: 7, first_name: 'Eve', username: 'eve', last_seen_at: 1_790_000_000, count: 3 },
        { tg_user_id: 42, first_name: 'Me', username: '', last_seen_at: 1, count: 1 },
      ]),
    });
    return renderWithStore(<BotSettings botId={1} />, api);
  };

  it('shows whitelist and rejected senders not already listed', async () => {
    setup();
    expect(await screen.findByText('我')).toBeTruthy();
    expect(screen.getByText('Eve')).toBeTruthy();
    expect(screen.getByText(/3 次/)).toBeTruthy();
    expect(screen.queryByText('Me')).toBeNull();
  });

  it('toggles can_fetch and whitelists rejected senders in one click', async () => {
    const { api } = setup();
    await screen.findByText('我');
    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: '允许 42 代取' }));
    });
    expect(api.putWhitelist).toHaveBeenCalledWith(1, 42, '我', true);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '将 7 加入白名单' }));
    });
    expect(api.putWhitelist).toHaveBeenCalledWith(1, 7, 'Eve', false);
  });

  it('validates and adds a whitelist entry', async () => {
    const { api } = setup();
    await screen.findByText('我');
    type('用户 ID', 'abc');
    fireEvent.click(screen.getByText('加入白名单'));
    expect(await screen.findByLabelText('请输入正确的用户 ID')).toBeTruthy();
    type('请输入正确的用户 ID', '1001');
    type('备注（可选）', ' 朋友 ');
    fireEvent.click(screen.getByText('允许代取受保护内容'));
    await act(async () => {
      fireEvent.click(screen.getByText('加入白名单'));
    });
    expect(api.putWhitelist).toHaveBeenCalledWith(1, 1001, '朋友', true);
  });

  it('reports an unknown bot once bots are loaded', async () => {
    renderWithStore(<BotSettings botId={99} />, fakeApi({ bots: vi.fn(async () => []) }));
    expect(await screen.findByText('机器人不存在')).toBeTruthy();
  });

  it('removes entries and deletes the bot with purge', async () => {
    const { api } = setup();
    await screen.findByText('我');
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '移除 42' }));
    });
    expect(api.deleteWhitelist).toHaveBeenCalledWith(1, 42);
    fireEvent.click(screen.getByText('删除机器人'));
    fireEvent.click(screen.getByText('同时删除该机器人的全部存档'));
    await act(async () => {
      fireEvent.click(screen.getByText('删除'));
    });
    expect(api.deleteBot).toHaveBeenCalledWith(1, true);
    await waitFor(() => expect(route.value).toEqual({ name: 'settings' }));
  });
});

describe('TelegramAppSettings', () => {
  it('validates and saves credentials', async () => {
    const api = fakeApi({ telegramApp: vi.fn(async () => ({ configured: true, api_id: 4242, server: { managed: true, state: 'restarting', error: 'exit 1' } })) });
    renderWithStore(<TelegramAppSettings />, api);
    expect(await screen.findByText('重启中：exit 1')).toBeTruthy();
    expect((screen.getByLabelText('api_id') as HTMLInputElement).value).toBe('4242');
    type('api_hash（已保存，修改时重新输入）', 'xyz');
    fireEvent.click(screen.getByText('保存'));
    expect(await screen.findByLabelText('api_hash 必须是 32 位十六进制字符串')).toBeTruthy();
    expect(api.saveTelegramApp).not.toHaveBeenCalled();
    type('api_hash 必须是 32 位十六进制字符串', '0123456789abcdef0123456789ABCDEF');
    await act(async () => {
      fireEvent.click(screen.getByText('保存'));
    });
    expect(api.saveTelegramApp).toHaveBeenCalledWith(4242, '0123456789abcdef0123456789ABCDEF');
  });
});

describe('UserbotSettings', () => {
  it('walks phone → code → password → ready', async () => {
    const api = fakeApi({
      userbot: vi.fn(async () => ub('logged_out')),
      userbotPhone: vi.fn(async () => ub('code_sent', { phone: '+8613800000000' })),
      userbotCode: vi.fn(async () => ub('password_needed', { phone: '+8613800000000' })),
      userbotPassword: vi.fn(async () => ub('ready', { phone: '+8613800000000', name: 'Shinya', tg_user_id: 5 })),
    });
    renderWithStore(<UserbotSettings />, api);
    await screen.findByLabelText('手机号（含国家代码）');
    type('手机号（含国家代码）', '+86 138 0000 0000');
    await act(async () => {
      fireEvent.click(screen.getByText('发送验证码'));
    });
    expect(api.userbotPhone).toHaveBeenCalledWith('+86 138 0000 0000');
    expect(screen.getByText(/\+8613800000000 的 Telegram 客户端/)).toBeTruthy();
    type('验证码', '12345');
    await act(async () => {
      fireEvent.click(screen.getByText('下一步'));
    });
    expect(api.userbotCode).toHaveBeenCalledWith('12345');
    type('密码', 'pw');
    await act(async () => {
      fireEvent.click(screen.getByText('登录'));
    });
    expect(api.userbotPassword).toHaveBeenCalledWith('pw');
    expect(screen.getByText('Shinya · +8613800000000 · ID 5')).toBeTruthy();
    expect(screen.getByText('登出')).toBeTruthy();
  });

  it('shows server-side input errors in the field', async () => {
    const api = fakeApi({
      userbot: vi.fn(async () => ub('code_sent', { phone: '+1' })),
      userbotCode: vi.fn(async () => Promise.reject(new ApiError(400, '验证码错误', null))),
    });
    renderWithStore(<UserbotSettings />, api);
    await screen.findByLabelText('验证码');
    type('验证码', '000');
    await act(async () => {
      fireEvent.click(screen.getByText('下一步'));
    });
    expect(await screen.findByLabelText('验证码错误')).toBeTruthy();
  });

  it('logs out after confirmation', async () => {
    const api = fakeApi({ userbot: vi.fn(async () => ub('ready', { name: 'Me' })) });
    renderWithStore(<UserbotSettings />, api);
    fireEvent.click(await screen.findByText('登出'));
    await act(async () => {
      fireEvent.click(screen.getAllByText('登出')[1]);
    });
    expect(api.userbotLogout).toHaveBeenCalled();
  });

  it('links to API credentials when unconfigured and polls while connecting', async () => {
    renderWithStore(<UserbotSettings />, fakeApi({ userbot: vi.fn(async () => ub('unconfigured')) }));
    fireEvent.click(await screen.findByText('填写 API 凭据'));
    expect(route.value).toEqual({ name: 'settings-telegram-app' });

    vi.useFakeTimers();
    const userbot = vi.fn().mockResolvedValueOnce(ub('connecting')).mockResolvedValue(ub('logged_out'));
    renderWithStore(<UserbotSettings />, fakeApi({ userbot }));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(userbot).toHaveBeenCalledTimes(1);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(USERBOT_POLL_MS);
    });
    expect(userbot).toHaveBeenCalledTimes(2);
    expect(screen.getByText('发送验证码')).toBeTruthy();
  });
});
