// 喵窝：后端还没做，先放占位
import { html } from '../lib.js';
import { Card } from '../ui.js';

export function Mesh() {
    return html`<${Card} title="喵窝">
        <div class="empty">
            <div style="font-size:17px;font-weight:750;color:var(--t1)">即将推出</div>
            <p class="hint">把 NAS 所在的局域网开放进主控的喵窝组网：在外面也能用喵窝里的设备访问 NAS 与家里的局域网。</p>
        </div>
    <//>`;
}
