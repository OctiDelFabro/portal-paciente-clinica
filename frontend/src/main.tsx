import { createRoot } from 'react-dom/client';
import './style.css';
function App() {
 return <main><p className="tag">Portal paciente · Una clínica</p><h1>Tu atención, en un solo lugar</h1><p>Buscá profesionales, gestioná turnos y consultá tu historia clínica.</p><p className="notice" role="status">Estructura inicial de la Entrega 1. Las funciones del portal todavía están en desarrollo.</p><section aria-label="Funciones previstas"><article><h2>Turnos</h2><p>Reservar, cancelar y reprogramar con 24 horas de anticipación.</p></article><article><h2>Profesionales</h2><p>Buscar médicos y especialidades de la clínica.</p></article><article><h2>Historia clínica</h2><p>Consultar registros con los permisos correspondientes.</p></article></section></main>;
}
const root = document.getElementById('root');
if (root) createRoot(root).render(<App />);

