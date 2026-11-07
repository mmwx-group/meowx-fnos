// 日志：内核与控制服务的日志，2 秒增量拉取；按来源筛选、清空。停在底部时自动跟随，往上翻时不拽回
import { html, useState, useEffect, useRef, useLayoutEffect } from '../lib.js';
import { get, del } from '../api.js';
import { toast } from '../store.js';
import { Card, Button, Segmented } from '../ui.js';
import { clockTime } from '../util.js';

const MAX_LINES = 3000;
const sources = { core: '内核', agent: '控制服务', mesh: '喵窝' };

// 内核（mihomo）的行是 time="…" level=info msg="…"：取出级别与正文
function parse(line) {
    const m = /level=(\w+)\s+msg="((?:[^"\\]|\\.)*)"/.exec(line.text);
    if (m) {
        return { level: m[1], text: m[2].replace(/\\"/g, '"').replace(/\\\\/g, '\\') };
    }
    const lower = line.text.toLowerCase();
    return { level: /失败|错误|error|fatal|panic/.test(lower) ? 'error' : /warn/.test(lower) ? 'warning' : 'info', text: line.text };
}

export function Logs({ active }) {
    const [lines, setLines] = useState([]);
    const [source, setSource] = useState('');
    const [error, setError] = useState('');
    const after = useRef(0);
    const box = useRef();
    const follow = useRef(true);

    const load = async () => {
        try {
            const r = (await get(`/logs?after=${after.current}`)) ?? [];
            setError('');
            if (r.length == 0) {
                return;
            }
            after.current = r[r.length - 1].seq;
            setLines((old) => {
                const next = old.concat(r.map((l) => ({ ...l, ...parse(l) })));
                return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next;
            });
        } catch (err) {
            setError(err.message);
        }
    };
    useEffect(() => {
        if (!active) {
            return;
        }
        load();
        const t = setInterval(() => !document.hidden && load(), 2000);
        return () => clearInterval(t);
    }, [active]);

    const shown = source ? lines.filter((l) => l.source == source) : lines;
    useLayoutEffect(() => {
        if (follow.current && box.current) {
            box.current.scrollTop = box.current.scrollHeight;
        }
    }, [shown.length, source, active]);
    const onScroll = () => {
        const el = box.current;
        follow.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    };
    const clear = async () => {
        await del('/logs');
        setLines([]);
        toast('已清空日志');
    };

    return html`<${Card} i=${0}>
        <div class="toolbar">
            <${Segmented} value=${source} onChange=${(s) => { follow.current = true; setSource(s); }}
                options=${[['', '全部'], ['core', '内核'], ['agent', '控制服务']]} />
            <span style="flex:1"></span>
            <${Button} kind="danger" onClick=${clear}>清空日志<//>
        </div>
        ${error && html`<p class="error-text">${error}</p>`}
        <div class="logbox" ref=${box} onScroll=${onScroll}>
            ${shown.length == 0 && html`<div class="muted">还没有日志。</div>`}
            ${shown.map((l) => html`<div key=${l.seq} class=${'logline is-' + l.level + ' is-' + l.source}>
                <span class="logline__time">${clockTime(l.time)}</span>
                <span class="logline__src">${sources[l.source] || l.source}</span>
                <span class="logline__text">${l.text}</span>
            </div>`)}
        </div>
    <//>`;
}
