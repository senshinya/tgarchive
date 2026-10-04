import { act, fireEvent, render, screen } from '@testing-library/preact';
import { describe, expect, it, vi } from 'vitest';
import { renderWithStore } from '../test/render';
import { Avatar } from './Avatar';
import { ContextMenu } from './ContextMenu';
import { InputField } from './InputField';
import { ConfirmDialog } from './Modal';
import { Switch } from './Switch';
import { Tabs } from './Tabs';
import { TOAST_MS, Toast } from './Toast';

describe('Avatar', () => {
  it('shows initials on the peer color without a photo', () => {
    const { container } = render(<Avatar name="Alice Liddell" peerId={44} size="large" />);
    const el = container.querySelector('.Avatar') as HTMLElement;
    expect(el.textContent).toBe('AL');
    expect(el.style.width).toBe('54px');
    expect(el.style.getPropertyValue('--avatar-color')).toBe('var(--peer-2)');
  });

  it('falls back to initials when the photo fails to load', () => {
    const { container } = render(<Avatar name="Bob" peerId={1} size="medium" src="/avatars/senders/1" />);
    const img = container.querySelector('img')!;
    expect(img.getAttribute('src')).toBe('/avatars/senders/1');
    fireEvent.error(img);
    expect(container.querySelector('img')).toBeNull();
    expect(container.textContent).toBe('B');
  });
});

describe('controls', () => {
  it('Switch reports the new state', () => {
    const onChange = vi.fn();
    render(<Switch checked={false} label="启用" onChange={onChange} />);
    fireEvent.click(screen.getByRole('switch', { name: '启用' }));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('InputField shows the error in place of the label', () => {
    const onInput = vi.fn();
    const { rerender } = render(<InputField label="手机号" value="" onInput={onInput} />);
    fireEvent.input(screen.getByLabelText('手机号'), { target: { value: '+86' } });
    expect(onInput).toHaveBeenCalledWith('+86');
    rerender(<InputField label="手机号" value="+86" onInput={onInput} error="手机号格式不正确" />);
    expect(screen.getByLabelText('手机号格式不正确').getAttribute('aria-invalid')).toBe('true');
  });

  it('Tabs marks the active tab and reports clicks', () => {
    const onChange = vi.fn();
    render(<Tabs items={[{ key: 'a', label: '媒体' }, { key: 'b', label: '文件' }]} active="a" onChange={onChange} />);
    expect(screen.getByRole('tab', { name: '媒体' }).getAttribute('aria-selected')).toBe('true');
    fireEvent.click(screen.getByRole('tab', { name: '文件' }));
    expect(onChange).toHaveBeenCalledWith('b');
  });
});

describe('overlays', () => {
  it('ConfirmDialog confirms, cancels and closes on Escape', () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    render(<ConfirmDialog title="删除存档" text="确定？" confirmLabel="删除" danger onConfirm={onConfirm} onClose={onClose} />);
    fireEvent.click(screen.getByText('删除'));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByText('取消'));
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('ContextMenu runs the chosen item and closes on outside click', () => {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(<ContextMenu x={10} y={10} onClose={onClose} items={[{ label: '复制文本', icon: null, onSelect }]} />);
    fireEvent.click(screen.getByRole('menuitem', { name: '复制文本' }));
    expect(onSelect).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.mouseDown(document.body);
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('Toast shows the store message and hides it after a while', async () => {
    vi.useFakeTimers();
    const { store } = renderWithStore(<Toast />);
    act(() => store.showToast('保存失败'));
    expect(screen.getByRole('status').textContent).toBe('保存失败');
    await act(async () => {
      vi.advanceTimersByTime(TOAST_MS);
    });
    expect(screen.queryByRole('status')).toBeNull();
    vi.useRealTimers();
  });
});
