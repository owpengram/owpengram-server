// Decorative palm fronds tucked into the bottom corners of the sign-in hero
// band, behind the monkey -- a hint of jungle to go with it, not a scene of
// its own. Each frond is a small fan of pointed leaflet blades (plain
// quadratic curves, generated once by hand, not an asset), two per side for
// depth: a smaller, paler cluster set back, and a bigger one in front closer
// to the card's edge. Colors come from theme variables, so both themes get a
// correct tint for free instead of a second drawing.
export function LoginHeroFoliage() {
  return (
    <svg
      className="login-hero-foliage"
      viewBox="0 0 600 200"
      preserveAspectRatio="xMidYMax slice"
      aria-hidden="true"
    >
      <g className="login-hero-foliage-back">
        <path d="M-6.0 202.0 Q2.4 178.6 -10.7 135.2 Q-17.6 180.0 -6.0 202.0 Z" />
        <path d="M-6.0 202.0 Q10.9 175.6 15.2 117.1 Q-8.5 170.7 -6.0 202.0 Z" />
        <path d="M-6.0 202.0 Q21.9 176.2 51.2 110.4 Q5.0 165.6 -6.0 202.0 Z" />
        <path d="M-6.0 202.0 Q23.2 190.5 61.0 145.8 Q10.4 175.2 -6.0 202.0 Z" />
        <path d="M-6.0 202.0 Q18.9 202.7 56.1 176.9 Q11.4 184.2 -6.0 202.0 Z" />
        <path d="M-10.0 200.0 Q6.7 198.1 25.6 175.1 Q-2.5 185.0 -10.0 200.0 Z" />
        <path d="M-10.0 200.0 Q15.1 200.3 56.6 178.4 Q10.2 185.0 -10.0 200.0 Z" />
        <path d="M-10.0 200.0 Q4.9 207.7 33.4 199.2 Q4.6 191.7 -10.0 200.0 Z" />
        <path d="M606.0 202.0 Q597.6 178.6 610.7 135.2 Q617.6 180.0 606.0 202.0 Z" />
        <path d="M606.0 202.0 Q589.1 175.6 584.8 117.1 Q608.5 170.7 606.0 202.0 Z" />
        <path d="M606.0 202.0 Q578.1 176.2 548.8 110.4 Q595.0 165.6 606.0 202.0 Z" />
        <path d="M606.0 202.0 Q576.8 190.5 539.0 145.8 Q589.6 175.2 606.0 202.0 Z" />
        <path d="M606.0 202.0 Q581.1 202.7 543.9 176.9 Q588.6 184.2 606.0 202.0 Z" />
        <path d="M610.0 200.0 Q593.3 198.1 574.4 175.1 Q602.5 185.0 610.0 200.0 Z" />
        <path d="M610.0 200.0 Q584.9 200.3 543.4 178.4 Q589.8 185.0 610.0 200.0 Z" />
        <path d="M610.0 200.0 Q595.1 207.7 566.6 199.2 Q595.4 191.7 610.0 200.0 Z" />
      </g>
      <g className="login-hero-foliage-front">
        <path d="M10.0 206.0 Q22.0 178.0 8.6 124.2 Q-3.0 178.4 10.0 206.0 Z" />
        <path d="M10.0 206.0 Q33.4 175.5 43.9 104.6 Q9.7 167.6 10.0 206.0 Z" />
        <path d="M10.0 206.0 Q47.5 178.3 91.3 102.0 Q27.8 162.9 10.0 206.0 Z" />
        <path d="M10.0 206.0 Q47.4 197.0 100.2 148.6 Q33.9 175.9 10.0 206.0 Z" />
        <path d="M10.0 206.0 Q39.9 211.9 89.7 187.6 Q34.3 187.6 10.0 206.0 Z" />
        <path d="M590.0 206.0 Q578.0 178.0 591.4 124.2 Q603.0 178.4 590.0 206.0 Z" />
        <path d="M590.0 206.0 Q566.6 175.5 556.1 104.6 Q590.3 167.6 590.0 206.0 Z" />
        <path d="M590.0 206.0 Q552.5 178.3 508.7 102.0 Q572.2 162.9 590.0 206.0 Z" />
        <path d="M590.0 206.0 Q552.6 197.0 499.8 148.6 Q566.1 175.9 590.0 206.0 Z" />
        <path d="M590.0 206.0 Q560.1 211.9 510.3 187.6 Q565.7 187.6 590.0 206.0 Z" />
      </g>
    </svg>
  );
}
