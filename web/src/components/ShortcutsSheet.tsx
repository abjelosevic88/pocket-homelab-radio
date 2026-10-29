import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['l'], 'Listen'], [['f'], 'Find stations'], [['s'], 'Your stations'], [['c'], 'Your channels'], [['h'], 'Songs you heard'], [['y'], 'History'], [[','], 'Settings'], [['/'], 'Play a station or run a command'], [['⌘', 'K'], 'Play a station or run a command']] },
  { title: 'Add', keys: [[['n'], 'Add a station by its address'], [['m'], 'Make a channel from your music']] },
  { title: 'Player', keys: [[['space'], 'Play or stop']] },
  { title: 'Window', keys: [[['['], 'Show or hide the sidebar'], [['esc'], 'Close a dialog or menu'], [['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
    </Modal>
  )
}
