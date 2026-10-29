// A station's logo (through the server's logo cache) or, without one, its
// initials on a colour picked from the name. Channels get their colour and
// the radio mark.
import { useState } from 'react'
import { logoSrc } from '../lib/player'
import type { Channel, Station } from '../lib/types'
import { Icon } from './icons'

const TONES = ['#2f7d6d', '#b45309', '#7c3aed', '#be123c', '#0369a1', '#4d7c0f', '#a21caf', '#c2410c', '#0f766e', '#4338ca']

export function toneOf(name: string) {
  let h = 0
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return TONES[h % TONES.length]
}

function letters(name: string) {
  const words = name.replace(/[^\p{L}\p{N} ]/gu, ' ').split(/\s+/).filter(Boolean)
  if (!words.length) return '♪'
  if (words.length === 1) return words[0].slice(0, 2).toUpperCase()
  return (words[0][0] + words[1][0]).toUpperCase()
}

export function StationLogo({ station, size = 44, className = '' }: { station: Pick<Station, 'name' | 'logo_url'>; size?: number; className?: string }) {
  const [broken, setBroken] = useState(false)
  const style = { width: size, height: size, fontSize: Math.round(size * 0.36) }
  if (station.logo_url && !broken) {
    return <span className={`st-logo ${className}`} style={style}><img src={logoSrc(station.logo_url)} alt="" loading="lazy" onError={() => setBroken(true)} /></span>
  }
  return <span className={`st-logo st-logo-text ${className}`} style={{ ...style, background: toneOf(station.name) }} aria-hidden>{letters(station.name)}</span>
}

export function ChannelLogo({ channel, size = 44, className = '' }: { channel: Pick<Channel, 'name' | 'color'>; size?: number; className?: string }) {
  return (
    <span className={`st-logo st-logo-channel ${className}`} style={{ width: size, height: size, background: channel.color || toneOf(channel.name) }} aria-hidden>
      <Icon name="radio" size={Math.round(size * 0.5)} strokeWidth={2} />
    </span>
  )
}
