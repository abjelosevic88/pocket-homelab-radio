// Listening history: how much, when and what — the last week, month,
// quarter or year — and every stretch of listening, which you can remove.
import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, V1 } from '../lib/api'
import { usePlayer } from '../lib/player'
import { fmtDate, fmtDateTime, fmtDuration } from '../lib/format'
import type { History, Listen, TopEntry } from '../lib/types'
import { Icon } from '../components/icons'
import { ChannelLogo, StationLogo, toneOf } from '../components/StationLogo'
import { useOnChange } from '../components/Layout'
import { Empty, Loading, MenuButton, PageHeader, Segmented, useToast } from '../components/ui'

type Range = 7 | 30 | 90 | 365

function hm(seconds: number) {
  const min = Math.round(seconds / 60)
  if (!min) return seconds > 0 ? 'under a minute' : '0 min'
  return fmtDuration(min)
}

/** Minutes per day (or per week for a year) as plain bars. */
function Bars({ days, weekly }: { days: History['days']; weekly: boolean }) {
  const buckets = useMemo(() => {
    if (!weekly) return days.map(d => ({ label: d.date, from: d.date, to: d.date, seconds: d.seconds }))
    const out: { label: string; from: string; to: string; seconds: number }[] = []
    for (let i = 0; i < days.length; i += 7) {
      const part = days.slice(i, i + 7)
      out.push({ label: part[0].date, from: part[0].date, to: part[part.length - 1].date, seconds: part.reduce((a, d) => a + d.seconds, 0) })
    }
    return out
  }, [days, weekly])
  const max = Math.max(60, ...buckets.map(b => b.seconds))
  const W = 700, H = 150
  const bw = (W - 8) / Math.max(1, buckets.length)
  const every = Math.ceil(buckets.length / (W > 500 ? 8 : 5))
  return (
    <div className="hs-bars">
      <svg viewBox={`0 0 ${W} ${H + 2}`} preserveAspectRatio="none" role="img" aria-label={weekly ? 'Minutes per week' : 'Minutes per day'}>
        <line x1={0} x2={W} y1={H} y2={H} className="hs-axis" />
        {buckets.map((b, i) => {
          const h = b.seconds ? Math.max(2, (b.seconds / max) * (H - 6)) : 0
          return (
            <g key={b.from}>
              <rect x={i * bw + 4 + bw * 0.12} y={H - h} width={Math.max(1, bw * 0.76)} height={h} rx={Math.min(3, bw * 0.2)} className="hs-bar">
                <title>{`${b.from === b.to ? fmtDate(b.from) : `${fmtDate(b.from, false)} – ${fmtDate(b.to)}`}: ${hm(b.seconds)}`}</title>
              </rect>
            </g>
          )
        })}
      </svg>
      <div className="hs-ticks" aria-hidden>
        {buckets.map((b, i) => i % every === 0 && <span key={b.from} style={{ left: `${((i * bw + 4 + bw / 2) / W) * 100}%` }}>{fmtDate(b.label, false)}</span>)}
      </div>
      <div className="hs-max muted xs">top {hm(max)}</div>
    </div>
  )
}

function Hours({ hours }: { hours: number[] }) {
  const max = Math.max(1, ...hours)
  const label = (h: number) => `${String(h).padStart(2, '0')}:00`
  return (
    <div className="hs-hours" role="img" aria-label="When you listen, by hour of the day">
      {hours.map((s, h) => (
        <div key={h} className="hs-hour" title={`${label(h)}–${label((h + 1) % 24)}: ${hm(s)}`}>
          <span className="hs-hour-track"><span className="hs-hour-fill" style={{ height: `${(s / max) * 100}%` }} /></span>
          <span className="hs-hour-l">{h % 6 === 0 ? h : ''}</span>
        </div>
      ))}
    </div>
  )
}

function TopRow({ e, max }: { e: TopEntry; max: number }) {
  return (
    <li className="hs-top-row">
      {e.kind === 'channel' ? <ChannelLogo channel={{ name: e.name, color: '' }} size={30} /> : <StationLogo station={{ name: e.name, logo_url: '' }} size={30} />}
      <div className="grow">
        <div className="row"><span className="ellipsis grow">{e.name}</span><span className="muted small tnum">{hm(e.seconds)}</span></div>
        <span className="hs-top-track"><span style={{ width: `${(e.seconds / max) * 100}%`, background: toneOf(e.name) }} /></span>
      </div>
    </li>
  )
}

export default function HistoryPage() {
  const { toast } = useToast()
  const p = usePlayer()
  const [range, setRange] = useState<Range>(30)
  const [data, setData] = useState<History | null>(null)
  const [error, setError] = useState('')
  const load = useCallback(() => {
    api.get<History>(`${V1}/history?days=${range}`).then(d => { setData(d); setError('') }).catch(e => setError((e as Error).message))
  }, [range])
  useEffect(() => { load() }, [load])
  useOnChange(load)
  // a listen that ended moves into the history
  useEffect(() => { if (p.status !== 'playing') { const t = window.setTimeout(load, 1500); return () => window.clearTimeout(t) } }, [p.status, load])

  const remove = async (l: Listen) => {
    try { await api.del(`${V1}/listens/${l.id}`); load() } catch (e) { toast((e as Error).message, 'err') }
  }

  const label = range === 7 ? 'the last 7 days' : range === 30 ? 'the last 30 days' : range === 90 ? 'the last 3 months' : 'the last year'
  return (
    <div className="hs-page">
      <PageHeader title="History" sub={`What you listened to in ${label}.`}
        actions={<a className="btn" href={`${V1}/export.csv`}><Icon name="download" size={15} />Export (CSV)</a>} />
      <div className="hd-toolbar">
        <Segmented value={range} onChange={v => setRange(v)} label="Range" options={[{ value: 7, label: '7 days' }, { value: 30, label: '30 days' }, { value: 90, label: '3 months' }, { value: 365, label: 'Year' }]} />
      </div>
      {error ? <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div> : !data ? <Loading /> : (
        <div className="stack loose">
          <div className="hs-stats">
            <div className="hs-stat"><span className="hs-stat-n">{hm(data.total_seconds)}</span><span className="hs-stat-l">listening</span></div>
            <div className="hs-stat"><span className="hs-stat-n tnum">{data.sessions.toLocaleString()}</span><span className="hs-stat-l">{data.sessions === 1 ? 'time' : 'times'}</span></div>
            <div className="hs-stat"><span className="hs-stat-n tnum">{data.heard_count.toLocaleString()}</span><span className="hs-stat-l">songs heard</span></div>
            <div className="hs-stat"><span className="hs-stat-n tnum">{data.saved_count.toLocaleString()}</span><span className="hs-stat-l">starred</span></div>
          </div>
          {data.sessions === 0 ? <Empty title="Nothing in this time" icon="chart">Play a station or one of your channels; everything longer than half a minute shows up here.</Empty> : <>
            <section className="card">
              <div className="card-head"><div className="grow"><h2>{range > 90 ? 'Each week' : 'Each day'}</h2></div></div>
              <div className="card-body"><Bars days={data.days} weekly={range > 90} /></div>
            </section>
            <div className="two-col">
              <section className="card">
                <div className="card-head"><div className="grow"><h2>Most played</h2></div></div>
                <div className="card-body">
                  <ul className="hs-top">{data.top.map(e => <TopRow key={e.kind + e.name} e={e} max={data.top[0]?.seconds || 1} />)}</ul>
                </div>
              </section>
              <section className="card">
                <div className="card-head"><div className="grow"><h2>When you listen</h2><p className="muted small">By the hour of the day.</p></div></div>
                <div className="card-body"><Hours hours={data.hours} /></div>
              </section>
            </div>
            <section className="card">
              <div className="card-head"><div className="grow"><h2>Every time you listened</h2>{data.listens.length < data.sessions && <p className="muted small">The latest {data.listens.length}.</p>}</div></div>
              <div className="card-body flush">
                <ul className="hs-listens">
                  {data.listens.map(l => (
                    <li key={l.id} className="hs-listen">
                      {l.channel_id ? <ChannelLogo channel={{ name: l.name, color: '' }} size={32} /> : <StationLogo station={{ name: l.name, logo_url: '' }} size={32} />}
                      <div className="grow">
                        <div className="ellipsis">{l.name}</div>
                        <div className="muted small ellipsis">{fmtDateTime(l.started_at)}{l.client === 'app' ? ' · another player' : l.client === 'import' ? ' · imported' : ''}</div>
                      </div>
                      <span className="muted small tnum">{hm(l.seconds)}</span>
                      <MenuButton label="More" align="right" className="btn ghost icon-only sm" items={[{ label: 'Remove from the history', icon: 'trash', danger: true, onSelect: () => remove(l) }]}><Icon name="more" size={16} /></MenuButton>
                    </li>
                  ))}
                </ul>
              </div>
            </section>
          </>}
        </div>
      )}
    </div>
  )
}
