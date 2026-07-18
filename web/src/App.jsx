import { useState } from 'react'

function App() {
  const [count, setCount] = useState(0)

  return (
    <div style={{ padding: '2rem', fontFamily: 'sans-serif' }}>
      <h1>PipelineGen Admin UI</h1>
      <p>Scaffolding iniziale completato.</p>
      <button onClick={() => setCount((c) => c + 1)}>
        count is {count}
      </button>
    </div>
  )
}

export default App
