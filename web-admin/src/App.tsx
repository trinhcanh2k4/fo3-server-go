import { useCallback, useEffect, useState } from 'react'
import './App.css'

type AnyMap = Record<string, any>

const TOKEN_KEY = 'fo3_admin_token'

async function api(path: string, method = 'GET', body?: AnyMap): Promise<AnyMap> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const token = localStorage.getItem(TOKEN_KEY)
  if (token) headers['X-Admin-Token'] = token
  const res = await fetch('/admin/api' + path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (res.status === 401) {
    localStorage.removeItem(TOKEN_KEY)
    location.reload()
  }
  return data
}

function fmt(n: number) {
  return (n ?? 0).toLocaleString('vi-VN')
}

function Login({ onDone }: { onDone: () => void }) {
  const [u, setU] = useState('admin')
  const [p, setP] = useState('')
  const [err, setErr] = useState('')
  const submit = async () => {
    const r = await api('/login', 'POST', { username: u, password: p })
    if (r.result) {
      localStorage.setItem(TOKEN_KEY, r.token)
      onDone()
    } else {
      setErr('Sai tài khoản hoặc mật khẩu admin.')
    }
  }
  return (
    <div className="login">
      <h1>FO3 Admin</h1>
      <input placeholder="Tài khoản admin" value={u} onChange={(e) => setU(e.target.value)} />
      <input
        type="password"
        placeholder="Mật khẩu admin"
        value={p}
        onChange={(e) => setP(e.target.value)}
        onKeyDown={(e) => e.key === 'Enter' && submit()}
      />
      {err && <div className="err">{err}</div>}
      <button onClick={submit}>Đăng nhập</button>
    </div>
  )
}

function NumberEditor({ label, value, onSave }: { label: string; value: number; onSave: (v: number) => void }) {
  const [v, setV] = useState(String(value))
  useEffect(() => setV(String(value)), [value])
  return (
    <label className="field">
      <span>{label}</span>
      <input value={v} onChange={(e) => setV(e.target.value)} inputMode="numeric" />
      <button onClick={() => onSave(parseInt(v.replace(/\D/g, ''), 10) || 0)}>Lưu</button>
    </label>
  )
}

const POS_NAMES = ['GK', 'SW', 'RWB', 'RB', 'RCB', 'CB', 'LCB', 'LB', 'LWB', 'RDM', 'CDM', 'LDM', 'RM', 'RCM', 'CM', 'LCM', 'LM', 'RAM', 'CAM', 'LAM', 'RF', 'CF', 'LF', 'RW', 'RS', 'ST', 'LS', 'LW', 'SUB']

function SquadPanel({ uid }: { uid: number }) {
  const [sq, setSq] = useState<AnyMap | null>(null)
  useEffect(() => {
    api(`/squad/${uid}/0`).then(setSq)
  }, [uid])
  if (!sq) return <div>Đang tải đội hình…</div>
  if (!sq.positions) return <div>Chưa có dữ liệu đội hình.</div>
  return (
    <div>
      <p>
        Sơ đồ: <b>{sq.formation}</b> · Đội trưởng (iid): {sq.captain || '—'}
      </p>
      <table>
        <thead>
          <tr>
            <th>Vị trí</th>
            <th>Cầu thủ</th>
            <th>SPID</th>
            <th>Mùa</th>
            <th>Grade</th>
            <th>Hợp đồng</th>
          </tr>
        </thead>
        <tbody>
          {(sq.positions || []).map((p: AnyMap, i: number) => (
            <tr key={i}>
              <td>{POS_NAMES[p.pos] || p.pos}</td>
              <td>{p.name || '—'}</td>
              <td>{p.spid}</td>
              <td>{p.season_name || '—'}</td>
              <td>{p.grade}</td>
              <td>{p.life}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <h5>Dự bị / ngoài đội hình ({(sq.reserves || []).length})</h5>
      <table>
        <thead>
          <tr>
            <th>Cầu thủ</th>
            <th>SPID</th>
            <th>Mùa</th>
            <th>Grade</th>
            <th>Đội</th>
          </tr>
        </thead>
        <tbody>
          {(sq.reserves || []).map((p: AnyMap) => (
            <tr key={p.iid}>
              <td>{p.name}</td>
              <td>{p.spid}</td>
              <td>{p.season_name || '—'}</td>
              <td>{p.grade}</td>
              <td>{p.team2 ? 'Đội 2' : 'Đội 1'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

const MODULE_LABELS: Record<string, string> = {
  quests: 'Nhiệm vụ',
  achievements: 'Thành tích',
  league: 'Đấu giải',
  cups: 'Cup',
  ranked: 'Xếp hạng',
  progress: 'Tiến trình',
}

function UserDetail({ user, reload }: { user: AnyMap; reload: () => void }) {
  const [utab, setUtab] = useState('tongquan')
  const [players, setPlayers] = useState<AnyMap[]>([])
  const [items, setItems] = useState<AnyMap[]>([])
  const [grantSpid, setGrantSpid] = useState('')
  const [grantGrade, setGrantGrade] = useState('1')
  const [staffs, setStaffs] = useState<AnyMap[]>([])
  const [grantStfid, setGrantStfid] = useState('')
  const [grantStaffGrade, setGrantStaffGrade] = useState('5')
  const [grantItem, setGrantItem] = useState('')
  const [grantCount, setGrantCount] = useState('1')
  const [giftEp, setGiftEp] = useState('')
  const [giftPack, setGiftPack] = useState('')
  const [giftCount, setGiftCount] = useState('1')
  const [vipDays, setVipDays] = useState('30')
  const [pcDays, setPcDays] = useState('30')
  const uid = user.id
  const coach = user.coaches?.[0]

  const loadSubs = useCallback(async () => {
    const pl = await api(`/players/${uid}/0`)
    setPlayers(pl.players || [])
    const inv = await api(`/inventory/${uid}/0`)
    setItems(inv.items || [])
    const sf = await api(`/staff/${uid}/0`)
    setStaffs(sf.staffs || [])
  }, [uid])
  useEffect(() => {
    loadSubs()
  }, [loadSubs])

  const set = (field: string, value: any, extra?: AnyMap) =>
    api(`/coaches/${uid}/0/set/${field}`, 'POST', { value, ...extra }).then(reload)
  const act = (action: string, body?: AnyMap) => api(`/users/${uid}/${action}`, 'POST', body || {}).then(reload)
  const reset = (module: string) => {
    if (confirm(`Reset module "${module}" của tài khoản này?`)) api(`/coaches/${uid}/0/reset`, 'POST', { module }).then(reload)
  }

  if (!coach) return <div>Chưa có HLV.</div>
  return (
    <div className="detail">
      <h3>
        Tài khoản {user.username} — HLV: {coach.name} · Đội: {coach.teamname || coach.teamid} (ID {coach.teamid})
      </h3>
      <div className="row subtabs">
        {[
          ['tongquan', 'Tổng quan'],
          ['cauthu', `Cầu thủ (${players.length})`],
          ['doihinh', 'Đội hình'],
          ['staff', `HLV huấn luyện (${staffs.length})`],
          ['kho', `Kho & Quà (${items.length})`],
        ].map(([k, label]) => (
          <button key={k} className={utab === k ? 'active' : ''} onClick={() => setUtab(k)}>
            {label}
          </button>
        ))}
      </div>
      {utab === 'tongquan' && (
        <>
      <div className="grid">
        <NumberEditor label="EP" value={coach.ep} onSave={(v) => set('ep', v)} />
        <NumberEditor label="GP (quest)" value={coach.gp} onSave={(v) => set('gp', v)} />
        <NumberEditor label="Elo XH 1v1" value={coach.elo} onSave={(v) => set('elo', v)} />
        <NumberEditor label="Elo Quản lý" value={coach.elo_mgr} onSave={(v) => set('elo', v, { mode: 'mgr' })} />
        <NumberEditor label="Cấp HLV" value={coach.level} onSave={(v) => set('level', v)} />
        <NumberEditor label="Huy hiệu" value={coach.badge} onSave={(v) => set('badge', v)} />
      </div>
      <p>
        Giá trị đội: <b>{fmt(coach.teamvalue)} EP</b> · Thắng/Hòa/Thua: {coach.wins}/{coach.draws}/{coach.losses}
      </p>
      <div className="row">
        <span>
          Quyền PC: <b>{(user.pc_until || 0) > Date.now() ? `Có (đến ${new Date(user.pc_until).toLocaleDateString('vi-VN')})` : 'Không'}</b>
          {' · '}VIP: <b>{(user.vip_until || 0) > Date.now() ? `Có (đến ${new Date(user.vip_until).toLocaleDateString('vi-VN')})` : 'Không'}</b>
        </span>
      </div>
      <div className="row">
        <span>Cấp quyền:</span>
        <input className="mini" value={pcDays} onChange={(e) => setPcDays(e.target.value)} />
        <span>ngày PC</span>
        <button onClick={() => act('entitlement', { pc_days: parseInt(pcDays, 10) || 0 })}>Cấp PC</button>
        <input className="mini" value={vipDays} onChange={(e) => setVipDays(e.target.value)} />
        <span>ngày VIP</span>
        <button onClick={() => act('entitlement', { vip_days: parseInt(vipDays, 10) || 0 })}>Cấp VIP</button>
        <button className="danger" onClick={() => confirm('Thu hồi cả PC và VIP của tài khoản này?') && act('entitlement', { pc_days: 0, vip_days: 0 })}>
          Thu hồi cả hai
        </button>
      </div>
      <div className="row">
        <button onClick={() => act(user.disabled ? 'enable' : 'disable')}>{user.disabled ? 'Mở khóa TK' : 'Khóa TK'}</button>
        <button
          onClick={() => {
            const pw = prompt('Mật khẩu mới cho tài khoản game:')
            if (pw) act('password', { password: pw })
          }}
        >
          Đặt lại mật khẩu
        </button>
        <button onClick={() => confirm('Xóa mật khẩu cấp 2?') && act('secondpass-reset')}>Reset MK cấp 2</button>
        <button className="danger" onClick={() => confirm('XÓA tài khoản này vĩnh viễn?') && act('delete').then(reload)}>
          Xóa TK
        </button>
      </div>
      <div className="row">
        <span>Đặt lại:</span>
        {['quests', 'achievements', 'league', 'cups', 'ranked', 'progress'].map((m) => (
          <button key={m} onClick={() => reset(m)}>
            {MODULE_LABELS[m] || m}
          </button>
        ))}
      </div>
        </>
      )}
      {utab === 'doihinh' && <SquadPanel uid={uid} />}
      {utab === 'cauthu' && (
        <>
      <h4>Cầu thủ ({players.length})</h4>
      <div className="row">
        <input placeholder="spid" value={grantSpid} onChange={(e) => setGrantSpid(e.target.value)} />
        <input placeholder="grade" value={grantGrade} onChange={(e) => setGrantGrade(e.target.value)} className="mini" />
        <button onClick={() => api(`/players/${uid}/0/grant`, 'POST', { spid: parseInt(grantSpid, 10), grade: parseInt(grantGrade, 10) || 1 }).then(loadSubs)}>
          Cấp thẻ
        </button>
      </div>
      <table>
        <thead>
          <tr>
            <th>Cầu thủ</th>
            <th>SPID</th>
            <th>Mùa</th>
            <th>Grade</th>
            <th>Hợp đồng</th>
            <th>Đội</th>
            <th>Giá</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {players.map((p) => (
            <tr key={p.iid}>
              <td>{p.name || '—'}</td>
              <td>{p.spid}</td>
              <td>{p.season_name || '—'}</td>
              <td>{p.grade}</td>
              <td>{p.life}</td>
              <td>{p.team2 ? 'Dự bị' : 'Chính'}</td>
              <td>{fmt(p.price)}</td>
              <td>
                <button onClick={() => api(`/players/${uid}/0/life`, 'POST', { iid: p.iid, value: 40 }).then(loadSubs)}>Gia hạn</button>
                <button className="danger" onClick={() => confirm('Xóa thẻ này?') && api(`/players/${uid}/0/remove`, 'POST', { iid: p.iid }).then(loadSubs)}>
                  Xóa
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
        </>
      )}
      {utab === 'staff' && (
        <>
      <h4>Staff ({staffs.length})</h4>
      <div className="row">
        <input placeholder="stfid (id trong catalog)" value={grantStfid} onChange={(e) => setGrantStfid(e.target.value)} />
        <input placeholder="grade" value={grantStaffGrade} onChange={(e) => setGrantStaffGrade(e.target.value)} className="mini" />
        <button onClick={() => api(`/staff/${uid}/0/grant`, 'POST', { stfid: parseInt(grantStfid, 10), grade: parseInt(grantStaffGrade, 10) || 1 }).then(loadSubs)}>
          Cấp staff
        </button>
      </div>
      <table>
        <thead>
          <tr>
            <th>Stfid</th>
            <th>Tên</th>
            <th>Vị trí</th>
            <th>Grade</th>
            <th>EXP</th>
            <th>Hợp đồng</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {staffs.map((s) => (
            <tr key={s.id}>
              <td>{s.stfid}</td>
              <td>{s.name}</td>
              <td>{s.position}</td>
              <td>{s.grade}</td>
              <td>{s.exp}</td>
              <td>{s.life}</td>
              <td>
                <button onClick={() => api(`/staff/${uid}/0/life`, 'POST', { staffiid: s.id, value: 100 }).then(loadSubs)}>Gia hạn</button>
                <button className="danger" onClick={() => confirm('Xóa staff này?') && api(`/staff/${uid}/0/remove`, 'POST', { staffiid: s.id }).then(loadSubs)}>
                  Xóa
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
        </>
      )}
      {utab === 'kho' && (
        <>
      <h4>Kho vật phẩm ({items.length})</h4>
      <div className="row">
        <input placeholder="itemid" value={grantItem} onChange={(e) => setGrantItem(e.target.value)} />
        <input placeholder="số lượng" value={grantCount} onChange={(e) => setGrantCount(e.target.value)} className="mini" />
        <button onClick={() => api(`/inventory/${uid}/0/grant`, 'POST', { itemid: parseInt(grantItem, 10), count: parseInt(grantCount, 10) || 1 }).then(loadSubs)}>
          Cấp vật phẩm
        </button>
      </div>
      <h4>Tặng gói quà (vào Túi quà)</h4>
      <div className="row">
        <input placeholder="EP trong gói" value={giftEp} onChange={(e) => setGiftEp(e.target.value)} className="mini" />
        <input placeholder="itemid gói/thẻ" value={giftPack} onChange={(e) => setGiftPack(e.target.value)} />
        <input placeholder="số lượng" value={giftCount} onChange={(e) => setGiftCount(e.target.value)} className="mini" />
        <button
          onClick={() =>
            api(`/inventory/${uid}/0/bag`, 'POST', {
              ep: parseInt(giftEp, 10) || 0,
              pack: parseInt(giftPack, 10) || 0,
              count: parseInt(giftCount, 10) || 1,
              title: 'Quà từ quản trị viên',
            }).then(() => alert('Đã gửi gói quà vào Túi quà của HLV'))
          }
        >
          Gửi gói quà
        </button>
      </div>
      <table>
        <thead>
          <tr>
            <th>ItemID</th>
            <th>Tên</th>
            <th>Số lượng</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {items.map((i) => (
            <tr key={i._id || i.id}>
              <td>{i.itemid}</td>
              <td>{i.staticdata?.name}</td>
              <td>{i.remains}</td>
              <td>
                <button className="danger" onClick={() => api(`/inventory/${uid}/0/remove`, 'POST', { itemid: i.itemid, count: i.remains }).then(loadSubs)}>
                  Thu hồi
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
        </>
      )}
    </div>
  )
}

function DailyEditor() {
  const [rows, setRows] = useState<AnyMap[]>([])
  const [custom, setCustom] = useState(false)
  useEffect(() => {
    api('/config/daily').then((r) => {
      setRows(r.table || [])
      setCustom(!!r.custom)
    })
  }, [])
  const save = () =>
    api('/config/daily', 'PUT', { table: rows.map((r) => ({ ep: +r.ep || 0, pack: +r.pack || 0, count: +r.count || 0 })) }).then(() => alert('Đã lưu bảng điểm danh'))
  return (
    <div>
      <h3>Điểm danh hàng ngày {custom ? '(bảng tùy chỉnh)' : '(bảng mặc định)'}</h3>
      <table>
        <thead>
          <tr>
            <th>Ngày</th>
            <th>EP</th>
            <th>ItemID gói</th>
            <th>Số lượng</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              <td>{i + 1}</td>
              {(['ep', 'pack', 'count'] as const).map((f) => (
                <td key={f}>
                  <input
                    className="mini"
                    value={r[f] ?? 0}
                    onChange={(e) => {
                      const next = rows.slice()
                      next[i] = { ...next[i], [f]: e.target.value }
                      setRows(next)
                    }}
                  />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <div className="row">
        <button onClick={() => setRows([...rows, { ep: 0, pack: 0, count: 0 }])}>Thêm ngày</button>
        <button onClick={save}>Lưu bảng điểm danh</button>
      </div>
    </div>
  )
}

function EnhanceEditor() {
  const [pct, setPct] = useState(0)
  const [rates, setRates] = useState<number[]>([])
  useEffect(() => {
    api('/config/enhance').then((r) => {
      setPct(r.success_percent || 0)
      setRates(r.default_rates || [])
    })
  }, [])
  return (
    <div>
      <h3>Cường hóa thẻ</h3>
      <p>
        Tỉ lệ mặc định theo cấp: {rates.map((r, i) => (i > 0 ? `+${i}: ${Math.round(r * 100)}%` : '')).filter(Boolean).join(' · ')}
      </p>
      <div className="row">
        <label>
          Ép tỉ lệ thành công (% — 0 = dùng bảng mặc định):{' '}
          <input className="mini" value={pct} onChange={(e) => setPct(parseInt(e.target.value, 10) || 0)} />
        </label>
        <button onClick={() => api('/config/enhance', 'PUT', { success_percent: pct }).then(() => alert('Đã lưu cấu hình cường hóa'))}>Lưu</button>
      </div>
    </div>
  )
}

function CraftEditor() {
  const [cfg, setCfg] = useState<AnyMap>({})
  const [players, setPlayers] = useState('')
  const [custom, setCustom] = useState(false)
  useEffect(() => {
    api('/config/craft').then((r) => {
      setCfg(r.config || {})
      setPlayers(((r.config || {}).players || []).join(','))
      setCustom(!!r.custom)
    })
  }, [])
  const save = () =>
    api('/config/craft', 'PUT', {
      players: players.split(',').map((x) => parseInt(x.trim(), 10)).filter((n) => !Number.isNaN(n)),
      count: +cfg.count || 30,
      pieces: +cfg.pieces || 10,
      fee: +cfg.fee || 0,
      grade: +cfg.grade || 1,
    }).then((r) => (r.result ? alert('Đã lưu cấu hình Ghép thẻ') : alert('Lỗi: ' + (r.reason || 'không lưu được'))))
  return (
    <div>
      <h3>Ghép thẻ (Craft) {custom ? '(đang tùy chỉnh)' : '(mặc định)'}</h3>
      <div className="row">
        <label>
          SPID cầu thủ đặc biệt (phẩy): <input className="wide" value={players} onChange={(e) => setPlayers(e.target.value)} />
        </label>
      </div>
      <div className="row">
        {(['count', 'pieces', 'fee', 'grade'] as const).map((f) => (
          <label key={f}>
            {f}: <input className="mini" value={cfg[f] ?? ''} onChange={(e) => setCfg({ ...cfg, [f]: e.target.value })} />
          </label>
        ))}
        <button onClick={save}>Lưu</button>
      </div>
    </div>
  )
}

function QuestEditor() {
  const [pool, setPool] = useState('')
  const [milestones, setMilestones] = useState('')
  const [rewards, setRewards] = useState('')
  const [custom, setCustom] = useState(false)
  const load = () =>
    api('/config/quests').then((r) => {
      setPool(JSON.stringify(r.pool || [], null, 1))
      setMilestones(JSON.stringify(r.milestones || [], null, 1))
      setRewards(JSON.stringify(r.rewards || {}, null, 1))
      setCustom(!!r.custom)
    })
  useEffect(() => { load() }, [])
  const save = () => {
    try {
      const body = { pool: JSON.parse(pool), milestones: JSON.parse(milestones), rewards: JSON.parse(rewards) }
      api('/config/quests', 'PUT', body).then((r) => {
        if (r.result) {
          alert('Đã lưu cấu hình nhiệm vụ')
          load()
        } else alert('Lỗi: ' + (r.reason || 'không lưu được'))
      })
    } catch {
      alert('JSON không hợp lệ')
    }
  }
  return (
    <div>
      <h3>Nhiệm vụ tuần + mốc GP + quà nhiệm vụ HLV {custom ? '(đang tùy chỉnh)' : '(mặc định)'}</h3>
      <p>Pool nhiệm vụ tuần: metric thuộc match/win/goal/rank/rankwin/upgrade. Rewards theo id nhiệm vụ HLV (0–14): ep / pack (itemid gói, 0 = không) / gp.</p>
      <div className="row">
        <textarea rows={7} cols={42} value={pool} onChange={(e) => setPool(e.target.value)} />
        <textarea rows={7} cols={42} value={milestones} onChange={(e) => setMilestones(e.target.value)} />
        <textarea rows={7} cols={42} value={rewards} onChange={(e) => setRewards(e.target.value)} />
      </div>
      <div className="row">
        <button onClick={save}>Lưu cấu hình nhiệm vụ</button>
        <button onClick={() => confirm('Xoá mọi tuỳ chỉnh nhiệm vụ, về mặc định?') && api('/config/quests', 'PUT', {}).then(load)}>Về mặc định</button>
      </div>
    </div>
  )
}

function AchieveEditor() {
  const [text, setText] = useState('')
  const [custom, setCustom] = useState(false)
  const [count, setCount] = useState(0)
  const load = () =>
    api('/config/achievements').then((r) => {
      const ov: AnyMap = {}
      for (const e of r.entries || []) {
        if (JSON.stringify(e.effective) !== JSON.stringify(e.authored)) ov[e.id] = e.effective
      }
      setText(JSON.stringify(ov, null, 1))
      setCount((r.entries || []).length)
      setCustom(!!r.custom)
    })
  useEffect(() => { load() }, [])
  const save = () => {
    try {
      const overrides = JSON.parse(text)
      api('/config/achievements', 'PUT', { overrides }).then((r) => {
        if (r.result) {
          alert('Đã lưu quà thành tích')
          load()
        } else alert('Lỗi: ' + (r.reason || 'không lưu được'))
      })
    } catch {
      alert('JSON không hợp lệ')
    }
  }
  return (
    <div>
      <h3>Quà thành tích ({count} mục) {custom ? '(đang tùy chỉnh)' : '(mặc định)'}</h3>
      <p>Map id thành tích → danh sách quà [{'{'}"itemid": 1, "count": 100000{'}'}] (itemid 1 = EP). Để {'{}'} là dùng quà gốc.</p>
      <div className="row">
        <textarea rows={7} cols={60} value={text} onChange={(e) => setText(e.target.value)} />
      </div>
      <div className="row">
        <button onClick={save}>Lưu quà thành tích</button>
      </div>
    </div>
  )
}

function GrantPanel({ users, reload }: { users: AnyMap[]; reload: () => void }) {
  const [userQuery, setUserQuery] = useState('')
  const [selUid, setSelUid] = useState<number | null>(null)
  const [selSlot, setSelSlot] = useState(0)
  const [staffQuery, setStaffQuery] = useState('')
  const [staffResults, setStaffResults] = useState<AnyMap[]>([])
  const [staffGrade, setStaffGrade] = useState('1')
  const [playerQuery, setPlayerQuery] = useState('')
  const [playerResults, setPlayerResults] = useState<AnyMap[]>([])
  const [grade, setGrade] = useState('1')
  const [msg, setMsg] = useState('')

  const selUser = users.find((u) => u.id === selUid)
  const coaches: AnyMap[] = selUser?.coaches || []
  const selCoach = coaches.find((c) => c.slot === selSlot) || coaches[0]

  const filteredUsers = users.filter((u) => {
    const q = userQuery.trim().toLowerCase()
    if (!q) return true
    return u.username.toLowerCase().includes(q) || (u.coaches || []).some((c: AnyMap) => (c.name || '').toLowerCase().includes(q))
  })

  const searchStaff = () => {
    api(`/search/staff?q=${encodeURIComponent(staffQuery)}`).then((r) => setStaffResults(r.staff || []))
  }
  const searchPlayers = () => {
    if (playerQuery.trim().length < 2) return
    api(`/search/players?q=${encodeURIComponent(playerQuery)}`).then((r) => setPlayerResults(r.players || []))
  }
  const grantStaff = (sf: AnyMap) => {
    if (!selUser || !selCoach) return
    api(`/staff/${selUser.id}/${selCoach.slot}/grant`, 'POST', { stfid: sf.stfid, grade: parseInt(staffGrade, 10) || 1 }).then((r) => {
      setMsg(r.result ? `Đã cấp HLV ${sf.name} cho đội của ${selCoach.name}` : `Lỗi: ${r.reason || 'không cấp được'}`)
      reload()
    })
  }
  const grantPlayer = (p: AnyMap) => {
    if (!selUser || !selCoach) return
    api(`/players/${selUser.id}/${selCoach.slot}/grant`, 'POST', { spid: p.spid, grade: parseInt(grade, 10) || 1 }).then((r) => {
      setMsg(r.result ? `Đã cấp ${p.name} cho HLV ${selCoach.name}` : `Lỗi: ${r.reason || 'không cấp được'}`)
      reload()
    })
  }

  return (
    <div>
      <h3>Cấp HLV huấn luyện & thẻ cầu thủ</h3>
      <div className="row">
        <input placeholder="Tìm tài khoản / tên HLV…" value={userQuery} onChange={(e) => setUserQuery(e.target.value)} className="wide" />
      </div>
      <div className="row">
        {filteredUsers.map((u) => (
          <button
            key={u.id}
            className={selUid === u.id ? 'active' : ''}
            onClick={() => {
              setSelUid(u.id)
              setSelSlot(u.coaches?.[0]?.slot ?? 0)
            }}
          >
            {u.username} — {(u.coaches || []).map((c: AnyMap) => c.name).join(', ')}
          </button>
        ))}
      </div>
      {msg && <p className="note">{msg}</p>}
      {selUser && (
        <>
          <div className="row subtabs">
            <span>HLV nhận:</span>
            {coaches.map((c) => (
              <button key={c.slot} className={selCoach?.slot === c.slot ? 'active' : ''} onClick={() => setSelSlot(c.slot)}>
                {c.name} · {c.teamname || c.teamid} · {c.players} cầu thủ
              </button>
            ))}
            {coaches.length === 0 && <span>Chưa có HLV nào</span>}
          </div>
          <h4>Cấp HLV huấn luyện (staff) cho đội của {selCoach?.name || '—'}</h4>
          <div className="row">
            <input
              placeholder="Tìm HLV huấn luyện theo tên… (để trống = xem tất cả)"
              value={staffQuery}
              onChange={(e) => setStaffQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && searchStaff()}
              className="wide"
            />
            <button onClick={searchStaff}>Tìm</button>
            <label>
              Grade: <input className="mini" value={staffGrade} onChange={(e) => setStaffGrade(e.target.value)} />
            </label>
          </div>
          {staffResults.length > 0 && (
            <table>
              <thead>
                <tr>
                  <th>HLV huấn luyện</th>
                  <th>Vị trí</th>
                  <th>STFID</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {staffResults.map((sf) => (
                  <tr key={sf.stfid}>
                    <td>{sf.name}</td>
                    <td>{sf.label || sf.position}</td>
                    <td>{sf.stfid}</td>
                    <td>
                      <button onClick={() => grantStaff(sf)}>Cấp HLV</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <h4>Cấp thẻ cầu thủ cho HLV {selCoach?.name || '—'}</h4>
          <div className="row">
            <input
              placeholder="Tìm cầu thủ theo tên (vd: Ronaldo, Messi…)…"
              value={playerQuery}
              onChange={(e) => setPlayerQuery(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && searchPlayers()}
              className="wide"
            />
            <button onClick={searchPlayers}>Tìm</button>
            <label>
              Grade: <input className="mini" value={grade} onChange={(e) => setGrade(e.target.value)} />
            </label>
          </div>
          {playerResults.length > 0 && (
            <table>
              <thead>
                <tr>
                  <th>Cầu thủ</th>
                  <th>SPID</th>
                  <th>Mùa</th>
                  <th>OVR</th>
                  <th>Giá</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {playerResults.map((p) => (
                  <tr key={p.spid}>
                    <td>{p.name}</td>
                    <td>{p.spid}</td>
                    <td>{p.season_name || p.season}</td>
                    <td>{p.ovr}</td>
                    <td>{fmt(p.price)}</td>
                    <td>
                      <button onClick={() => grantPlayer(p)}>Cấp thẻ</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </div>
  )
}

function Shop() {
  const [kind, setKind] = useState('packs')
  const [entries, setEntries] = useState<AnyMap[]>([])
  const [filter, setFilter] = useState('')
  const load = useCallback(async () => {
    const r = await api(`/shop/${kind}`)
    setEntries(r.entries || [])
  }, [kind])
  useEffect(() => {
    load()
  }, [load])
  const shown = entries.filter((e) => !filter || String(e.itemid).includes(filter) || (e.name || '').toLowerCase().includes(filter.toLowerCase()))
  return (
    <div>
      <div className="row">
        {[
          ['packs', 'Gói thẻ'],
          ['items', 'Vật phẩm'],
          ['packages', 'Gói combo'],
          ['gp', 'Shop GP'],
        ].map(([k, label]) => (
          <button key={k} className={kind === k ? 'on' : ''} onClick={() => setKind(k)}>
            {label}
          </button>
        ))}
        <input placeholder="Lọc theo id/tên" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <span>{shown.length} mục</span>
      </div>
      <table>
        <thead>
          <tr>
            <th>ItemID</th>
            <th>Tên</th>
            <th>Giá hiện tại</th>
            <th>Giá admin đặt</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          {shown.slice(0, 300).map((e) => (
            <tr key={e.itemid}>
              <td>{e.itemid}</td>
              <td>
                {e.name}
                {e.season_label ? ` · Mùa: ${e.season_label}` : ''}
                {e.hidden ? ' · 🚫 Đã gỡ khỏi shop' : ''}
              </td>
              <td>{fmt(e.price)}</td>
              <td>{e.override ? fmt(e.override) : '—'}</td>
              <td>
                <button
                  onClick={() => {
                    const v = prompt(`Giá mới cho ${e.itemid} (để trống = bỏ đặt giá):`, e.override ? String(e.override) : '')
                    if (v !== null) api(`/shop/${kind === 'gp' ? 'gp' : 'ep'}/price`, 'PUT', { itemid: e.itemid, price: v === '' ? null : parseInt(v, 10) }).then(load)
                  }}
                >
                  Đặt giá
                </button>{' '}
                {kind !== 'gp' && (
                  <button onClick={() => api('/shop/hide', 'POST', { itemid: e.itemid, hidden: !e.hidden }).then(load)}>
                    {e.hidden ? 'Bán lại' : 'Gỡ khỏi shop'}
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function SeasonsEditor() {
  const [seasons, setSeasons] = useState<AnyMap>({})
  const load = useCallback(async () => {
    const r = await api('/settings')
    try {
      setSeasons(JSON.parse(r.settings?.seasons || '{}'))
    } catch {
      setSeasons({})
    }
  }, [])
  useEffect(() => {
    load()
  }, [load])
  const field = (label: string, key: string, def: string) => (
    <label>
      {label}{' '}
      <input type="date" value={seasons[key] || def} onChange={(e) => setSeasons({ ...seasons, [key]: e.target.value })} />
    </label>
  )
  return (
    <div>
      <h3>Mùa giải (ngày bắt đầu / kết thúc)</h3>
      <div className="row">
        {field('Xếp hạng từ', 'ladder_start', '2026-11-01')}
        {field('Xếp hạng đến', 'ladder_end', '2026-12-31')}
        {field('Cup từ', 'cup_start', '2026-11-01')}
        {field('Cup đến', 'cup_end', '2026-12-31')}
        <button onClick={() => api('/settings/seasons', 'PUT', { value: seasons }).then(load)}>Lưu mùa giải</button>
      </div>
      <p>Áp dụng vào màn Xếp hạng (ngày mùa) và danh sách Cup (hạn tham gia). Tối đa 1 phút để server nạp lại.</p>
    </div>
  )
}

function SimpleTable({ cols, rows }: { cols: string[]; rows: any[][] }) {
  return (
    <table>
      <thead>
        <tr>
          {cols.map((c) => (
            <th key={c}>{c}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>
            {r.map((v, j) => (
              <td key={j}>{v}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  )
}

export default function App() {
  // On the admin subdomain, the Nginx basic auth in front of
  // the panel replaces this login entirely (no second password).
  const [logged, setLogged] = useState(
    location.hostname.startsWith('fo3-adm.') || !!localStorage.getItem(TOKEN_KEY),
  )
  const [tab, setTab] = useState('users')
  const [users, setUsers] = useState<AnyMap[]>([])
  const [sel, setSel] = useState<number | null>(null)
  const [ops, setOps] = useState<AnyMap>({})
  const [online, setOnline] = useState<AnyMap>({})
  const [ladder, setLadder] = useState<AnyMap[]>([])
  const [clubs, setClubs] = useState<AnyMap[]>([])
  const [telemetry, setTelemetry] = useState<AnyMap[]>([])
  const [audit, setAudit] = useState<AnyMap[]>([])
  const [settings, setSettings] = useState<AnyMap>({})
  const [notice, setNotice] = useState('')
  const [backups, setBackups] = useState<AnyMap[]>([])
  const [maint, setMaint] = useState(false)

  const loadUsers = useCallback(async () => {
    const r = await api('/users')
    setUsers(r.users || [])
  }, [])
  useEffect(() => {
    if (!logged) return
    loadUsers()
    api('/ops').then((r) => setOps(r.ops || {}))
    api('/online').then(setOnline)
    api('/ladder').then((r) => setLadder(r.rows || []))
    api('/clubs').then((r) => setClubs(r.clubs || []))
    api('/telemetry?hours=24').then((r) => setTelemetry(r.errors || []))
    api('/audit?limit=100').then((r) => setAudit(r.audit || []))
    api('/ops/backups').then((r) => setBackups(r.backups || []))
    api('/settings').then((r) => {
      setSettings(r.settings || {})
      try {
        setMaint(JSON.parse(r.settings?.maintenance || '{}').on === true)
      } catch {
        setMaint(false)
      }
    })
  }, [logged, loadUsers, tab])

  if (!logged) return <Login onDone={() => setLogged(true)} />

  const tabs: [string, string][] = [
    ['users', 'Tài khoản & HLV'],
    ['grant', 'Cấp HLV & Thẻ'],
    ['shop', 'Shop & giá'],
    ['ladder', 'Xếp hạng'],
    ['clubs', 'Clan'],
    ['telemetry', 'Log lỗi game'],
    ['audit', 'Nhật ký thao tác'],
    ['settings', 'Cài đặt & vận hành'],
  ]
  const selected = users.find((u) => u.id === sel)
  return (
    <div className="layout">
      <aside>
        <h2>FO3 Admin</h2>
        <div className="status">
          <div>
            MariaDB: {ops.mariadb ? '✅' : '❌'} · Redis: {ops.redis ? '✅' : '❌'}
          </div>
          <div>
            Online: {online.online?.length ?? 0} HLV
          </div>
          <div>Tài khoản: {ops.users ?? '—'}</div>
        </div>
        {tabs.map(([k, label]) => (
          <button key={k} className={tab === k ? 'nav on' : 'nav'} onClick={() => setTab(k)}>
            {label}
          </button>
        ))}
        <button
          className="nav"
          onClick={() => {
            localStorage.removeItem(TOKEN_KEY)
            location.reload()
          }}
        >
          Đăng xuất
        </button>
      </aside>
      <main>
        {tab === 'users' && (
          <div>
            <div className="row">
              <h3>Danh sách tài khoản</h3>
              <button
                onClick={async () => {
                  const username = prompt('Username mới:')
                  const password = prompt('Mật khẩu:')
                  const coach_name = prompt('Tên HLV:')
                  const teamid = prompt('TeamID CLB (vd 9 = Liverpool):')
                  if (username && password && coach_name && teamid)
                    api('/users', 'POST', { username, password, coach_name, teamid: parseInt(teamid, 10) }).then(loadUsers)
                }}
              >
                + Tạo tài khoản
              </button>
            </div>
            <SimpleTable
              cols={['ID', 'Username', 'HLV', 'EP', 'Elo', 'Cấp', 'Trạng thái', '']}
              rows={users.map((u) => [
                u.id,
                u.username,
                u.coaches?.[0]?.name || '',
                fmt(u.coaches?.[0]?.ep || 0),
                u.coaches?.[0]?.elo || '',
                u.coaches?.[0]?.level || '',
                u.disabled ? 'Đã khóa' : 'Hoạt động',
                <button key={u.id} onClick={() => setSel(sel === u.id ? null : u.id)}>
                  {sel === u.id ? 'Đóng' : 'Quản lý'}
                </button>,
              ])}
            />
            {selected && <UserDetail user={selected} reload={loadUsers} />}
          </div>
        )}
        {tab === 'grant' && <GrantPanel users={users} reload={loadUsers} />}
        {tab === 'shop' && <Shop />}
        {tab === 'ladder' && (
          <div>
            <div className="row">
              <h3>Bảng xếp hạng 1v1</h3>
              <button className="danger" onClick={() => confirm('Reset toàn bộ mùa xếp hạng?') && api('/ladder/reset-season', 'POST', {})}>
                Reset mùa
              </button>
            </div>
            <SimpleTable cols={['HLV', 'Tài khoản', 'Elo', 'Thắng', 'Hòa', 'Thua']} rows={ladder.map((r) => [r.coach, r.username, r.elo, r.wins, r.draws, r.losses])} />
          </div>
        )}
        {tab === 'clubs' && (
          <div>
            <h3>Clan / Team</h3>
            <SimpleTable
              cols={['ClubID', 'Tên', 'Chủ clan', 'Thành viên', '']}
              rows={clubs.map((c) => [
                c.clubid,
                c.name,
                c.master_id,
                c.members,
                <button key={c.clubid} className="danger" onClick={() => confirm(`Giải tán clan ${c.name}?`) && api(`/clubs/${c.clubid}/dissolve`, 'POST', {})}>
                  Giải tán
                </button>,
              ])}
            />
          </div>
        )}
        {tab === 'telemetry' && (
          <div>
            <h3>Lỗi giao diện client (24h)</h3>
            <SimpleTable
              cols={['Thời gian', 'User', 'URI', 'Loại', 'Thông điệp']}
              rows={telemetry.map((t) => [new Date(t.at * 1000).toLocaleString('vi-VN'), t.user_id, t.uri, t.category, (t.message || '').slice(0, 120)])}
            />
          </div>
        )}
        {tab === 'audit' && (
          <div>
            <h3>Nhật ký thao tác</h3>
            <SimpleTable
              cols={['Thời gian', 'Admin', 'Hành động', 'Đối tượng', 'Chi tiết']}
              rows={audit.map((a) => [new Date(a.at * 1000).toLocaleString('vi-VN'), a.admin, a.action, a.target, (a.detail || '').slice(0, 100)])}
            />
          </div>
        )}
        {tab === 'settings' && (
          <div>
            <h3>Vận hành</h3>
            <div className="row">
              <label>
                <input
                  type="checkbox"
                  checked={maint}
                  onChange={(e) => {
                    setMaint(e.target.checked)
                    api('/settings/maintenance', 'PUT', { value: { on: e.target.checked, message: 'Server đang bảo trì, vui lòng quay lại sau.' } })
                  }}
                />
                Chế độ bảo trì (chặn đăng nhập game)
              </label>
            </div>
            <div className="row">
              <input placeholder="Thông báo hệ thống tới mọi HLV đang online" value={notice} onChange={(e) => setNotice(e.target.value)} className="wide" />
              <button onClick={() => api('/notice', 'POST', { text: notice }).then(() => setNotice(''))}>Gửi</button>
            </div>
            <div className="row">
              <button onClick={() => api('/ops/backup', 'POST', {}).then(() => api('/ops/backups').then((r) => setBackups(r.backups || [])))}>
                Tạo backup MariaDB
              </button>
              {backups.slice(0, 5).map((b) => (
                <span key={b.name}>
                  {b.name} ({Math.round(b.size / 1024)} KB){' '}
                  <button
                    className="danger"
                    onClick={() =>
                      confirm(`Khôi phục database từ ${b.name}? Server sẽ tự backup trạng thái hiện tại trước khi khôi phục.`) &&
                      api('/ops/restore', 'POST', { name: b.name }).then((r) =>
                        r.result
                          ? alert(`Đã khôi phục từ ${r.name}. Bản backup trước khôi phục: ${r.pre_restore_backup}. Hãy Restart fo3-go để nạp lại dữ liệu.`)
                          : alert('Khôi phục lỗi: ' + (r.reason || '')),
                      )
                    }
                  >
                    Khôi phục
                  </button>
                </span>
              ))}
            </div>
            <div className="row">
              <span>Khởi động lại:</span>
              {['fo3-go', 'fo3', 'fo3-messenger'].map((svc) => (
                <button
                  key={svc}
                  className="danger"
                  onClick={() => confirm(`Restart ${svc}?`) && api('/ops/restart', 'POST', { service: svc })}
                >
                  {svc}
                </button>
              ))}
            </div>
            <DailyEditor />
            <EnhanceEditor />
            <SeasonsEditor />
            <CraftEditor />
            <QuestEditor />
            <AchieveEditor />
            <h3>Cài đặt (JSON)</h3>
            {Object.entries(settings).map(([k, v]) => (
              <div key={k} className="row">
                <code>{k}</code>
                <input
                  defaultValue={v}
                  className="wide"
                  onBlur={(e) => {
                    try {
                      api(`/settings/${k}`, 'PUT', { value: JSON.parse(e.target.value) })
                    } catch {
                      alert('JSON không hợp lệ')
                    }
                  }}
                />
              </div>
            ))}
          </div>
        )}
      </main>
    </div>
  )
}
