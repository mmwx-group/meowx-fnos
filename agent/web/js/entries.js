// 主控指定入口（入口覆盖，规格 miaomiaowuC/Design/entry-realtime.md §2.2 / §6）：内核 GET /meowx/entries 的回包 → 节点页的小标。
// 纯函数，不依赖界面框架
import { relativeTime } from './util.js';

// 回包（{entries:[…]}）→「节点名 → 条目」的 Map（节点名是任意字符串，不用普通对象：免得撞上 toString 这类原型上的名字）。
// 只认生效中（active）、有入口 IP、带 nodes（用到这条覆盖的节点名）的条目；
// 老内核没有 nodes 字段、接口报错（传进来 null）、内核没起时得到空表，界面什么都不显示
export function entryIndex(resp) {
    const list = Array.isArray(resp) ? resp : Array.isArray(resp?.entries) ? resp.entries : [];
    const index = new Map();
    for (const e of list) {
        if (!e || e.active !== true || !Array.isArray(e.ips) || e.ips.length == 0 || !Array.isArray(e.nodes)) {
            continue;
        }
        for (const name of e.nodes) {
            if (typeof name == 'string' && !index.has(name)) {
                index.set(name, e);
            }
        }
    }
    return index;
}

// 一条覆盖里给人看的两部分：各入口的写法、切换时间。
// 主控给了入口服务器名称（names：IP → 名称，只有管理员账号登录主控时才有）的写「名称（IP）」，没有的只写 IP
function entryParts(entry) {
    const names = entry.names && typeof entry.names == 'object' ? entry.names : {};
    const ips = entry.ips.map((ip) => (typeof names[ip] == 'string' && names[ip] ? `${names[ip]}（${ip}）` : ip));
    // updatedAt 是毫秒（本机换上这份入口的时刻）；一分钟以内 relativeTime 给「刚刚」，写成「刚刚切换」
    const ago = +entry.updatedAt > 0 ? relativeTime(entry.updatedAt / 1000) : '';
    return { ips, at: !ago ? '' : ago == '刚刚' ? '刚刚切换' : `切换于 ${ago}` };
}

// 一行的提示文字（title）：「主控指定入口 香港入口 A（203.0.113.11）, 203.0.113.12 · 切换于 3 分钟前」
export function entryTip(entry) {
    const { ips, at } = entryParts(entry);
    return `主控指定入口 ${ips.join(', ')}${at ? ' · ' + at : ''}`;
}

// 点开后格子里分行写的同一份内容：标题、每个入口一行、切换时间
export function entryLines(entry) {
    const { ips, at } = entryParts(entry);
    return ['主控指定入口', ...ips, at].filter(Boolean);
}
