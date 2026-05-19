import PocketBase from 'pocketbase';

const pocketBase = new PocketBase(window.location.origin);

export default function App() {
  return (
    <main className="scaffold-shell">
      <section className="scaffold-panel">
        <p className="scaffold-label">Editable scaffold</p>
        <h1>__SITE_TITLE__</h1>
        <p className="scaffold-copy">
          이 사이트는 아직 사용자 요청에 맞게 제작되기 전의 기본 작업 공간입니다. DESIGN.md를 작성하고 React 소스를 구현한 뒤 빌드해서 배포하세요.
        </p>
        <span className="scaffold-origin">{pocketBase.baseURL}</span>
      </section>
    </main>
  );
}
