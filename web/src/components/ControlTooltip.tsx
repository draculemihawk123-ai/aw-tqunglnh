import { useEffect, useState } from 'react';

/** Fixed positioning keeps keyboard tooltips outside clipped navigation islands. */
export function ControlTooltip() {
  const [tip, setTip] = useState<{ text: string; left: number; top: number } | null>(null);
  useEffect(() => {
    const show = (event: Event) => {
      const button = (event.target as Element)?.closest?.('button');
      const text = button?.getAttribute('aria-label') || button?.title;
      if (!button || !text || !button.querySelector('svg') || button.innerText.trim()) { setTip(null); return; }
      const rect = button.getBoundingClientRect();
      setTip({ text, left: Math.max(8, Math.min(rect.left, window.innerWidth - 264)), top: rect.bottom + 36 < window.innerHeight ? rect.bottom + 6 : rect.top - 34 });
    };
    const hide = () => setTip(null);
    document.addEventListener('focusin', show);
    document.addEventListener('mouseover', show);
    document.addEventListener('focusout', hide);
    document.addEventListener('mouseout', hide);
    document.addEventListener('click', hide);
    return () => {
      document.removeEventListener('focusin', show);
      document.removeEventListener('mouseover', show);
      document.removeEventListener('focusout', hide);
      document.removeEventListener('mouseout', hide);
      document.removeEventListener('click', hide);
    };
  }, []);
  return tip ? <div role="tooltip" className="fixed z-[100] pointer-events-none max-w-64 rounded-[6px] border border-[#CDD5DF] bg-white px-2 py-1 text-[12px] text-[#172033] shadow-sm" style={{ left: tip.left, top: tip.top }}>{tip.text}</div> : null;
}
