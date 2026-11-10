// 节点延迟测试地址（设置 test_url）：规则同客户端的 LatencyTestURL。纯函数，不依赖界面框架
//   空 =「跟随订阅」：测某个组（整组测速、组里点单个节点）用这个组写的测速地址（内核回的 testUrl），没写的用内置默认；
//   填了就一律用它。

export const DEFAULT_TEST_URL = 'https://www.gstatic.com/generate_204';

// [地址, 名字]：与 iOS / macOS 客户端「代理与测速」的预设同一组，全是 https（http 地址在被劫持的网络里会测出假延迟）
export const TEST_URL_PRESETS = [
    ['https://cp.cloudflare.com/generate_204', 'Cloudflare'],
    ['https://www.gstatic.com/generate_204', 'Google'],
    ['https://connectivitycheck.gstatic.com/generate_204', 'Google 连通性检查'],
    ['https://captive.apple.com/hotspot-detect.html', 'Apple'],
];

// 实际用哪个地址测：setting = 设置里的 test_url，groupUrl = 组的 testUrl
export function effectiveTestUrl(setting, groupUrl) {
    return String(setting ?? '').trim() || String(groupUrl ?? '').trim() || DEFAULT_TEST_URL;
}

// 选择框里该选中哪一项：'' = 跟随订阅，预设地址原样，其余 = 'custom'
export function testUrlChoice(setting) {
    const s = String(setting ?? '').trim();
    if (!s) {
        return '';
    }
    return TEST_URL_PRESETS.some(([url]) => url == s) ? s : 'custom';
}

// 校验自定义地址（与控制服务的 NormalizeTestURL 同一套）：回 { url } 或 { error }
export function checkTestUrl(text) {
    const s = String(text ?? '').trim();
    if (!s) {
        return { error: '请输入测速地址' };
    }
    if (s.length > 2048) {
        return { error: '测速地址太长' };
    }
    if (/\s/.test(s)) {
        return { error: '测速地址里不能有空格' };
    }
    if (!/^https?:\/\//i.test(s)) {
        return { error: '测速地址只能是 http:// 或 https:// 开头的网址' };
    }
    let u;
    try {
        u = new URL(s);
    } catch {
        return { error: '测速地址不是合法的网址' };
    }
    if (!u.hostname || !/^https?:\/\/[^/?#]/i.test(s)) {
        return { error: '测速地址缺少主机名' };
    }
    return { url: s };
}

// 最近一次延迟：按实际用的地址取内核记的历史（proxy.extra[url].history）。没测过 = null，0 = 超时 / 失败。
// 老内核没有 extra 时退回通用记录
export function lastDelay(proxy, url) {
    if (!proxy) {
        return null;
    }
    const hist = (proxy.extra ? proxy.extra[url]?.history : proxy.history) ?? [];
    return hist.length ? hist[hist.length - 1].delay : null;
}
