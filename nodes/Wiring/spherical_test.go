package Wiring

import (
	"math"
	"math/rand"
	"testing"
)

// ---------------------------------------------------------------------------
// Cartesian ORACLE — test-only. Production code (spherical.go) is angle-only;
// here we use vectors + Rodrigues to independently check the spherical trig.
// ---------------------------------------------------------------------------

func dirToVec(d dir) vec3 {
	return polar2cart(polar{R: 1, Theta: d.Theta, Phi: d.Phi})
}

func vecToDir(v vec3) dir {
	p := cart2polar(v)
	return dir{Theta: p.Theta, Phi: p.Phi}
}

func cross(a, b vec3) vec3 {
	return vec3{
		X: a.Y*b.Z - a.Z*b.Y,
		Y: a.Z*b.X - a.X*b.Z,
		Z: a.X*b.Y - a.Y*b.X,
	}
}

func dot(a, b vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }

// rodrigues rotates v by angle (right-hand) about unit axis k.
func rodrigues(v, k vec3, angle float64) vec3 {
	k = k.normalize()
	cosA, sinA := math.Cos(angle), math.Sin(angle)
	return v.scale(cosA).
		add(cross(k, v).scale(sinA)).
		add(k.scale(dot(k, v) * (1 - cosA)))
}

// ---------------------------------------------------------------------------

func TestAngularDistanceMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const tol = 1e-9
	for i := 0; i < 2000; i++ {
		a := randDir(rng)
		b := randDir(rng)
		got := angularDistance(a, b)
		va, vb := dirToVec(a), dirToVec(b)
		want := math.Acos(clamp(dot(va, vb), -1, 1))
		if math.Abs(got-want) > tol {
			t.Fatalf("angularDistance(%v,%v)=%v want %v", a, b, got, want)
		}
	}
}

func TestRotateDirMatchesRodrigues(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	const tol = 1e-7
	for i := 0; i < 5000; i++ {
		p := randDir(rng)
		axis := randDir(rng)
		angle := (rng.Float64()*2 - 1) * math.Pi
		got := rotateDir(p, axis, angle)
		want := vecToDir(rodrigues(dirToVec(p), dirToVec(axis), angle))
		if angularDistance(got, want) > tol {
			t.Fatalf("rotateDir(%v,axis=%v,angle=%v)=%v want %v (Δ=%v)",
				p, axis, angle, got, want, angularDistance(got, want))
		}
	}
}

func TestRotateDirIdentities(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 2000; i++ {
		p := randDir(rng)
		axis := randDir(rng)
		// Zero angle = no-op (within the round-trip's float precision).
		if d := angularDistance(rotateDir(p, axis, 0), p); d > 1e-6 {
			t.Fatalf("rotateDir zero-angle moved p by %v", d)
		}
		// Full turn returns home.
		if d := angularDistance(rotateDir(p, axis, 2*math.Pi), p); d > 1e-7 {
			t.Fatalf("rotateDir 2π moved p by %v", d)
		}
		// Rotating about p itself leaves p fixed.
		if d := angularDistance(rotateDir(p, p, rng.Float64()*math.Pi), p); d > 1e-7 {
			t.Fatalf("rotateDir about self moved p by %v", d)
		}
	}
}

func TestRotateDirPoleAxes(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	const tol = 1e-7
	yUp := dir{Theta: 0, Phi: 0}
	yDown := dir{Theta: math.Pi, Phi: 0}
	for i := 0; i < 2000; i++ {
		p := randDir(rng)
		angle := (rng.Float64()*2 - 1) * math.Pi
		for _, axis := range []dir{yUp, yDown} {
			got := rotateDir(p, axis, angle)
			want := vecToDir(rodrigues(dirToVec(p), dirToVec(axis), angle))
			if angularDistance(got, want) > tol {
				t.Fatalf("rotateDir about pole %v: got %v want %v", axis, got, want)
			}
		}
	}
}

func TestArcBetweenCarriesFromToTo(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	const tol = 1e-7
	for i := 0; i < 5000; i++ {
		from := randDir(rng)
		to := randDir(rng)
		r := arcBetween(from, to)
		if d := math.Abs(r.Angle - angularDistance(from, to)); d > tol {
			t.Fatalf("arcBetween angle %v != distance %v", r.Angle, angularDistance(from, to))
		}
		landed := rotateDir(from, r.Axis, r.Angle)
		if d := angularDistance(landed, to); d > tol {
			t.Fatalf("arcBetween(%v,%v) landed at %v (Δ=%v)", from, to, landed, d)
		}
	}
}

func randDir(rng *rand.Rand) dir {
	// Uniform-ish over the sphere; θ in (0,π), φ in (-π,π].
	return dir{
		Theta: math.Acos(clamp(rng.Float64()*2-1, -1, 1)),
		Phi:   (rng.Float64()*2 - 1) * math.Pi,
	}
}
