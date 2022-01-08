/*

       John Walker's Floating Point Benchmark, derived from...

             Marinchip Interactive Lens Design System
                    John Walker   December 1980

   This  program may be used, distributed, and modified freely as
   long as the origin information is preserved.

   This is a complete optical design raytracing algorithm,
   stripped of its user interface and recast into Go.
   It not only determines execution speed on an extremely
   floating point (including trig function) intensive
   real-world application, it checks accuracy on an algorithm
   that is exquisitely sensitive to errors. The performance of
   this program is typically far more sensitive to changes in
   the efficiency of the trigonometric library routines than the
   average floating point program.

   Implemented in July 2013 by John Nagle (http://www.animats.com).
   Slightly modified to be used as a function instead of command.

*/
package benchmarks

import "fmt"
import "time"
import . "math" // Put all exported functions in local namespace

func cot(x float64) float64 {
	return (1.0 / Tan(x))
}

const max_surfaces = 10

/*  Local variables  */

var current_surfaces int16
var paraxial int16

var clear_aperture float64

var aberr_lspher float64
var aberr_osc float64
var aberr_lchrom float64

var max_lspher float64
var max_osc float64
var max_lchrom float64

var radius_of_curvature float64
var object_distance float64
var ray_height float64
var axis_slope_angle float64
var from_index float64
var to_index float64

var spectral_line [9]float64
var s [max_surfaces][5]float64
var od_sa [2][2]float64

var outarr [8]string /* Computed output of program goes here */

var itercount int /* The iteration counter for the main loop
   in the program is made global so that
   the compiler should not be allowed to
   optimise out the loop over the ray
   tracing code. */

const ITERATIONS = 10000

var niter int = ITERATIONS /* Iteration counter */

/* Reference results.  These happen to
   be derived from a run on Microsoft
   Quick BASIC on the IBM PC/AT. */

var refarr0 = "   Marginal ray          47.09479120920   0.04178472683"
var refarr1 = "   Paraxial ray          47.08372160249   0.04177864821"
var refarr2 = "Longitudinal spherical aberration:        -0.01106960671"
var refarr3 = "    (Maximum permissible):                 0.05306749907"
var refarr4 = "Offense against sine condition (coma):     0.00008954761"
var refarr5 = "    (Maximum permissible):                 0.00250000000"
var refarr6 = "Axial chromatic aberration:                0.00448229032"
var refarr7 = "    (Maximum permissible):                 0.05306749907"

var refarr [8]string = [8]string{refarr0, refarr1, refarr2, refarr3, refarr4, refarr5, refarr6, refarr7}

/* The test case used in this program is the design for a 4 inch
   f/12 achromatic telescope objective used as the example in Wyld's
   classic work on ray tracing by hand, given in Amateur Telescope
   Making, Volume 3 (Volume 2 in the 1996 reprint edition). */

var testcase [4][4]float64 = [4][4]float64{
	[4]float64{27.05, 1.5137, 63.6, 0.52},
	[4]float64{-16.68, 1, 0, 0.138},
	[4]float64{-16.68, 1.6164, 36.7, 0.38},
	[4]float64{-78.1, 1, 0, 0},
}

/*           Calculate passage through surface

If the variable paraxial is 1, the trace through the
surface will be done using the paraxial approximations.
Otherwise, the normal trigonometric trace will be done.

This routine takes the following inputs:

radius_of_curvature      Radius of curvature of surface
                         being crossed.  If 0, surface is plane.

object_distance          Distance of object focus from
                         lens vertex.  If 0, incoming
                         rays are parallel and
                         the following must be specified:

ray_height               Height of ray from axis.  Only
                         relevant if OBJECT.DISTANCE == 0

axis_slope_angle         Angle incoming ray makes with axis
                         at intercept

from_index               Refractive index of medium being left

to_index                 Refractive index of medium being entered.

The outputs are the following variables:

object_distance          Distance from vertex to object focus
                         after refraction.

axis_slope_angle         Angle incoming ray makes with axis
                         at intercept after refraction.

*/

func transit_surface() {
	var iang float64
	var rang float64     /* Refraction angle */
	var iang_sin float64 /* Incidence angle sin */
	var rang_sin float64 /* Refraction angle sin */
	var old_axis_slope_angle float64
	var sagitta float64

	if paraxial > 0 {
		if radius_of_curvature != 0.0 {
			if object_distance == 0.0 {
				axis_slope_angle = 0.0
				iang_sin = ray_height / radius_of_curvature
			} else {
				iang_sin = ((object_distance -
					radius_of_curvature) / radius_of_curvature) *
					axis_slope_angle
			}

			rang_sin = (from_index / to_index) *
				iang_sin
			old_axis_slope_angle = axis_slope_angle
			axis_slope_angle = axis_slope_angle +
				iang_sin - rang_sin
			if object_distance != 0.0 {
				ray_height = object_distance * old_axis_slope_angle
			}
			object_distance = ray_height / axis_slope_angle
			return
		}
		object_distance = object_distance * (to_index / from_index)
		axis_slope_angle = axis_slope_angle * (from_index / to_index)
		return
	}

	if radius_of_curvature != 0.0 {
		if object_distance == 0.0 {
			axis_slope_angle = 0.0
			iang_sin = ray_height / radius_of_curvature
		} else {
			iang_sin = ((object_distance -
				radius_of_curvature) / radius_of_curvature) *
				Sin(axis_slope_angle)
		}
		iang = Asin(iang_sin)
		rang_sin = (from_index / to_index) *
			iang_sin
		old_axis_slope_angle = axis_slope_angle
		axis_slope_angle = axis_slope_angle +
			iang - Asin(rang_sin)
		sagitta = Sin((old_axis_slope_angle + iang) / 2.0)
		sagitta = 2.0 * radius_of_curvature * sagitta * sagitta
		object_distance = ((radius_of_curvature * Sin(
			old_axis_slope_angle+iang)) *
			cot(axis_slope_angle)) + sagitta
		return
	}

	rang = -Asin((from_index / to_index) *
		Sin(axis_slope_angle))
	object_distance = object_distance * ((to_index *
		Cos(-rang)) / (from_index *
		Cos(axis_slope_angle)))
	axis_slope_angle = -rang
}

/*  Perform ray trace in specific spectral line  */

func trace_line(line int, ray_h float64) {

	var i int16

	object_distance = 0.0
	ray_height = ray_h
	from_index = 1.0

	for i = 1; i <= current_surfaces; i++ {
		radius_of_curvature = s[i][1]
		to_index = s[i][2]
		if to_index > 1.0 {
			to_index = to_index + ((spectral_line[4]-
				spectral_line[line])/
				(spectral_line[3]-spectral_line[6]))*((s[i][2]-1.0)/
				s[i][3])
		}
		transit_surface()
		from_index = to_index
		if i < current_surfaces {
			object_distance = object_distance - s[i][4]
		}
	}
}

/*  Initialise when called the first time  */

func fbench() float64 {
	var errors int32
	var od_fline float64
	var od_cline float64

	spectral_line[1] = 7621.0   /* A */
	spectral_line[2] = 6869.955 /* B */
	spectral_line[3] = 6562.816 /* C */
	spectral_line[4] = 5895.944 /* D */
	spectral_line[5] = 5269.557 /* E */
	spectral_line[6] = 4861.344 /* F */
	spectral_line[7] = 4340.477 /* G'*/
	spectral_line[8] = 3968.494 /* H */

	niter = 1000000

	/* Load test case into working array */

	clear_aperture = 4.0
	current_surfaces = 4
	var i int16
	for i = 0; i < current_surfaces; i++ {
		for j := 0; j < 4; j++ {
			{
				s[i+1][j+1] = testcase[i][j]
			}
		}
	}

	starttime := time.Now() // start timing

	/* Perform ray trace the specified number of times. */

	for itercount = 0; itercount < niter; itercount++ {

		for paraxial = 0; paraxial <= 1; paraxial++ {

			/* Do main trace in D light */

			trace_line(4, clear_aperture/2.0)
			od_sa[paraxial][0] = object_distance
			od_sa[paraxial][1] = axis_slope_angle
		}
		paraxial = 0

		/* Trace marginal ray in C */

		trace_line(3, clear_aperture/2.0)
		od_cline = object_distance

		/* Trace marginal ray in F */

		trace_line(6, clear_aperture/2.0)
		od_fline = object_distance

		// Compute aberrations of the design

		/* The longitudinal spherical aberration is just the
		   difference between where the D line comes to focus
		   for paraxial and marginal rays. */
		aberr_lspher = od_sa[1][0] - od_sa[0][0]

		/* The offense against the sine condition is a measure
		   of the degree of coma in the design.  We compute it
		   as the lateral distance in the focal plane between
		   where a paraxial ray and marginal ray in the D line
		   come to focus. */
		aberr_osc = 1.0 - (od_sa[1][0]*od_sa[1][1])/
			(Sin(od_sa[0][1])*od_sa[0][0])

		/* The axial chromatic aberration is the distance between
		   where marginal rays in the C and F lines come to focus. */
		aberr_lchrom = od_fline - od_cline

		// Compute maximum acceptable values for each aberration

		max_lspher = Sin(od_sa[0][1])

		/* Maximum longitudinal spherical aberration, which is
		   also the maximum for axial chromatic aberration.  This
		   is computed for the D line. */
		max_lspher = 0.0000926 / (max_lspher * max_lspher)
		max_lchrom = max_lspher
		max_osc = 0.0025 // Max sine condition offence is constant
	}

	elapsedtime := time.Since(starttime) // timing

	/* Now evaluate the accuracy of the results from the last ray trace */

	outarr[0] = fmt.Sprintf("%15s   %21.11f  %14.11f",
		"Marginal ray", od_sa[0][0], od_sa[0][1])
	outarr[1] = fmt.Sprintf("%15s   %21.11f  %14.11f",
		"Paraxial ray", od_sa[1][0], od_sa[1][1])
	outarr[2] = fmt.Sprintf(
		"Longitudinal spherical aberration:      %16.11f",
		aberr_lspher)
	outarr[3] = fmt.Sprintf(
		"    (Maximum permissible):              %16.11f",
		max_lspher)
	outarr[4] = fmt.Sprintf(
		"Offense against sine condition (coma):  %16.11f",
		aberr_osc)
	outarr[5] = fmt.Sprintf(
		"    (Maximum permissible):              %16.11f",
		max_osc)
	outarr[6] = fmt.Sprintf(
		"Axial chromatic aberration:             %16.11f",
		aberr_lchrom)
	outarr[7] = fmt.Sprintf(
		"    (Maximum permissible):              %16.11f",
		max_lchrom)

	/* Now compare the edited results with the master values from
	   reference executions of this program. */

	errors = 0
	for i = 0; i < 8; i++ {
		if outarr[i] != refarr[i] {
			fmt.Printf("\nError in results on line %d...\n", i+1)
			fmt.Printf("Expected:  \"%s\"\n", refarr[i])
			fmt.Printf("Received:  \"%s\"\n", outarr[i])
			fmt.Printf("(Errors)    ")
			k := len(refarr[i])
			for j := 0; j < k; j++ {
				if refarr[i][j] == outarr[i][j] {
					fmt.Printf(" ")
				} else {
					fmt.Printf("^") // indicate character where data did not compare.
				}
				if refarr[i][j] != outarr[i][j] {
					errors++
				}
			}
			fmt.Printf("\n")
		}
	}
	if errors > 0 {
		plural := ""
		if errors > 1 {
			plural = "s"
		}
		fmt.Printf("\n%d error%s in results.  This is VERY SERIOUS.\n",
			errors, plural)

		return -1
	} else {
		return elapsedtime.Seconds()
	}
}
